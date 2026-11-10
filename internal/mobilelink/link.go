package mobilelink

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sylvinus/agent-vm/internal/relay"
	"github.com/sylvinus/agent-vm/internal/remotelink"
	"golang.org/x/net/http2"
)

const OfficialRelay = "https://43.142.88.90"

func relayOrigin(base string) (*url.URL, error) {
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Path != "" ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		(u.Scheme != "https" && (u.Scheme != "http" || !loopback(u.Hostname()))) {
		return nil, errors.New("relay must use an HTTPS origin or a local loopback HTTP origin")
	}
	return u, nil
}

func validCapability(value string) bool {
	b, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == value
}

// ValidRelayOrigin verifies the address before saving device configuration.
func ValidRelayOrigin(base string) bool {
	_, err := relayOrigin(base)
	return err == nil
}

// Retryable reports whether the host may establish a new transport room.
// It never authorizes retrying an application request.
type retryableError struct{ error }

// ErrNoInvitation covers offline, occupied, and unknown pairings equally.
var ErrNoInvitation = errors.New("no available room for the saved device")

func Retryable(err error) bool {
	var transient retryableError
	return errors.As(err, &transient)
}

func relayNetworkError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return retryableError{errors.New("relay request timed out")}
		}
		return ctx.Err()
	}
	var trust *tls.CertificateVerificationError
	if errors.As(err, &trust) {
		return errors.New("relay HTTPS certificate verification failed")
	}
	return retryableError{errors.New("could not connect to relay")}
}

func relayHTTPError(status int, missingIsTransient bool) error {
	err := fmt.Errorf("relay returned HTTP %d", status)
	if status == http.StatusConflict || status == http.StatusTooManyRequests || status == http.StatusRequestTimeout ||
		status >= 500 || (missingIsTransient && status == http.StatusNotFound) {
		return retryableError{err}
	}
	return err
}

type ClientInvitation struct {
	ID          string    `json:"id"`
	ClientToken string    `json:"client_token"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func relayRequest(ctx context.Context, base, method, path, token string, payload []byte, want int) ([]byte, error) {
	if _, err := relayOrigin(base); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(payload))
	if err != nil {
		return nil, errors.New("invalid relay origin")
	}
	req.GetBody = nil
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if len(payload) != 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	res, err := transport.RoundTrip(req)
	if err != nil {
		return nil, relayNetworkError(ctx, err)
	}
	defer res.Body.Close()
	if res.StatusCode != want {
		if path == "/v1/invitation" && res.StatusCode == http.StatusNotFound {
			return nil, ErrNoInvitation
		}
		return nil, relayHTTPError(res.StatusCode, path == "/v1/invitation")
	}
	return limited(res.Body, 4096)
}

// CreateRoom exchanges the relay administrator token for two transport roles.
// The host keeps the admin and host tokens. Only the client role is exported.
func CreateRoom(ctx context.Context, base, admin string) (relay.Invitation, error) {
	return createRoom(ctx, base, admin, nil)
}

// CreateDiscoverableRoom registers a hash, never the long-lived lookup bearer.
// A currently paired room cannot be displaced by a second registration.
func CreateDiscoverableRoom(ctx context.Context, base, admin, lookupToken string) (relay.Invitation, error) {
	if !validCapability(lookupToken) {
		return relay.Invitation{}, errors.New("invalid saved connection token")
	}
	raw, _ := base64.RawURLEncoding.DecodeString(lookupToken)
	digest := sha256.Sum256(raw)
	clear(raw)
	payload, _ := json.Marshal(map[string]string{"lookup_hash": hex.EncodeToString(digest[:])})
	return createRoom(ctx, base, admin, payload)
}

func createRoom(ctx context.Context, base, admin string, payload []byte) (relay.Invitation, error) {
	var invite relay.Invitation
	if (admin != "" && len(admin) < 32) || len(admin) > 4096 || strings.ContainsAny(admin, " \t\r\n") {
		return invite, errors.New("invalid relay administration token")
	}
	data, err := relayRequest(ctx, base, http.MethodPost, "/v1/rooms", admin, payload, http.StatusCreated)
	if err != nil {
		return invite, err
	}
	defer clear(data)
	if err != nil || json.Unmarshal(data, &invite) != nil || !validCapability(invite.ID) ||
		!validCapability(invite.HostToken) || !validCapability(invite.ClientToken) || !invite.ExpiresAt.After(time.Now()) {
		return relay.Invitation{}, errors.New("invalid relay invitation")
	}
	return invite, nil
}

// LookupRoom retrieves the current one-use client role for a saved pairing.
// The host fingerprint is supplied independently to ConnectClient.
func LookupRoom(ctx context.Context, base, lookupToken string) (ClientInvitation, error) {
	var room ClientInvitation
	if !validCapability(lookupToken) {
		return room, errors.New("invalid saved connection token")
	}
	data, err := relayRequest(ctx, base, http.MethodGet, "/v1/invitation", lookupToken, nil, http.StatusOK)
	if err != nil {
		return room, err
	}
	defer clear(data)
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&room) != nil || dec.Decode(new(any)) != io.EOF || !validCapability(room.ID) || !validCapability(room.ClientToken) || !room.ExpiresAt.After(time.Now()) {
		return ClientInvitation{}, errors.New("invalid saved connection invitation")
	}
	return room, nil
}

func join(ctx context.Context, base, roomID, role, token string) (*websocket.Conn, error) {
	u, err := relayOrigin(base)
	if err != nil {
		return nil, err
	}
	if !validCapability(roomID) || !validCapability(token) {
		return nil, errors.New("invalid relay room credentials")
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = "/v1/rooms/" + roomID + "/" + role
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	socket, res, err := dialer.DialContext(ctx, u.String(), http.Header{"Authorization": {"Bearer " + token}})
	if err != nil {
		if res != nil {
			res.Body.Close()
			return nil, relayHTTPError(res.StatusCode, true)
		}
		return nil, relayNetworkError(ctx, err)
	}
	return socket, nil
}

// ServeHost waits for an authenticated device and serves this workbench only.
// All requests pass the canonical mobile route filter and local Cage gates.
func ServeHost(ctx context.Context, base, roomID, hostToken string, identity *remotelink.Identity, expectedPin string, b *Workbench) error {
	if b == nil {
		return errors.New("local workbench session required")
	}
	ws, err := join(ctx, base, roomID, "host", hostToken)
	if err != nil {
		return err
	}
	conn, err := remotelink.ServerAfterJoin(ctx, ws, identity, expectedPin)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return retryableError{errors.New("room expired, disconnected, or peer authentication failed")}
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.NetConn().Close() })
	defer stop()
	defer conn.NetConn().Close()
	hostHandler := b.handler()
	server := &http2.Server{MaxConcurrentStreams: 16}
	server.ServeConn(conn, &http2.ServeConnOpts{Context: ctx, BaseConfig: &http.Server{
		MaxHeaderBytes: 16 << 10, IdleTimeout: 5 * time.Minute, ErrorLog: log.New(io.Discard, "", 0),
	}, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "cage-mobile.invalid" {
			http.Error(w, "invalid mobile authority", http.StatusForbidden)
			return
		}
		hostHandler.ServeHTTP(w, r)
	})})
	return ctx.Err()
}

type Client struct {
	session *http2.ClientConn
	conn    *tls.Conn
}

// ConnectClient opens a single authenticated HTTP/2 connection. expectedPin
// must come from an independently verified trust record on this device.
func ConnectClient(ctx context.Context, base, roomID, clientToken string, identity *remotelink.Identity, expectedPin string) (*Client, error) {
	ws, err := join(ctx, base, roomID, "client", clientToken)
	if err != nil {
		return nil, err
	}
	conn, err := remotelink.Client(ctx, ws, identity, expectedPin)
	if err != nil {
		return nil, errors.New("paired host authentication failed")
	}
	transport := &http2.Transport{MaxHeaderListSize: 16 << 10, ReadIdleTimeout: 15 * time.Second, PingTimeout: 8 * time.Second}
	session, err := transport.NewClientConn(conn)
	if err != nil {
		conn.NetConn().Close()
		return nil, errors.New("mobile protocol initialization failed")
	}
	return &Client{session: session, conn: conn}, nil
}

func (c *Client) Do(ctx context.Context, method, path string, body []byte) (*Reply, error) {
	return request(ctx, c.session, linkOrigin, method, path, body, false)
}

// Disconnected reports a retired transport, including a peer that sent GOAWAY.
func (c *Client) Disconnected() bool {
	state := c.session.State()
	return state.Closed || state.Closing
}

func (c *Client) Close() error {
	_ = c.conn.NetConn().Close()
	return c.session.Close()
}
