// Package nativeclient has a gomobile-bindable API for a native application.
// Platform code supplies the device signer and its previously verified host
// pin. Relay room tokens and the native signer stay outside the WebView.
package nativeclient

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/sylvinus/agent-vm/internal/mobilelink"
	"github.com/sylvinus/agent-vm/internal/remotelink"
	"golang.org/x/sync/singleflight"
)

// DeviceSigner signs SHA-256 digests with the same non-exportable P-256 key
// that produced PublicKeyDER. Signatures use ASN.1 DER (X9.62), not raw r||s.
type DeviceSigner interface {
	PublicKeyDER() ([]byte, error)
	SignSHA256(digest []byte) ([]byte, error)
}

type signerAdapter struct {
	device DeviceSigner
	public *ecdsa.PublicKey
}

func signer(device DeviceSigner) (*signerAdapter, error) {
	if device == nil {
		return nil, errors.New("missing device signer")
	}
	der, err := device.PublicKeyDER()
	if err != nil {
		return nil, errors.New("cannot read device public key")
	}
	key, err := x509.ParsePKIXPublicKey(der)
	pub, ok := key.(*ecdsa.PublicKey)
	if err != nil || !ok || pub.Curve != elliptic.P256() {
		return nil, errors.New("device public key must be a P-256 SubjectPublicKeyInfo")
	}
	return &signerAdapter{device, pub}, nil
}

func (s *signerAdapter) Public() crypto.PublicKey { return s.public }
func (s *signerAdapter) Sign(_ io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	if opts.HashFunc() != crypto.SHA256 || len(digest) != sha256.Size {
		return nil, errors.New("unsupported device signature algorithm")
	}
	return s.device.SignSHA256(digest)
}

// Fingerprint identifies the platform key that the host must explicitly trust
// through a channel independent of the relay.
func Fingerprint(device DeviceSigner) (string, error) {
	adapter, err := signer(device)
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalPKIXPublicKey(adapter.public)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(der)
	return "sha256:" + base64.StdEncoding.EncodeToString(digest[:]), nil
}

type Client struct {
	link        *mobilelink.Client
	recoverLink func(context.Context) (*mobilelink.Client, error)
	recovery    singleflight.Group
	lifetime    context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	closed      bool
	server      *http.Server
	listener    net.Listener
	browser     *mobilelink.Browser
}

// Open connects to a pre-provisioned room. The native app owns relay trust,
// peer pin persistence, first-time pairing, and its foreground lifecycle.
func Open(relayOrigin, roomID, clientToken, trustedHostPin string, device DeviceSigner) (*Client, error) {
	adapter, err := signer(device)
	if err != nil {
		return nil, err
	}
	identity, err := remotelink.IdentityFromSigner(adapter)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	link, err := mobilelink.ConnectClient(ctx, relayOrigin, roomID, clientToken, identity, trustedHostPin)
	if err != nil {
		return nil, err
	}
	return &Client{link: link}, nil
}

// OpenSaved resolves a fresh room for an already verified host. It does not
// send any application operation, including during recovery after disconnect.
func OpenSaved(relayOrigin, lookupToken, trustedHostPin string, device DeviceSigner) (*Client, error) {
	adapter, err := signer(device)
	if err != nil {
		return nil, err
	}
	identity, err := remotelink.IdentityFromSigner(adapter)
	if err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithCancel(context.Background())
	dial := func(parent context.Context) (*mobilelink.Client, error) {
		ctx, stop := context.WithTimeout(parent, 25*time.Second)
		defer stop()
		room, err := lookupSaved(ctx, relayOrigin, lookupToken)
		if err != nil {
			return nil, err
		}
		return mobilelink.ConnectClient(ctx, relayOrigin, room.ID, room.ClientToken, identity, trustedHostPin)
	}
	link, err := dial(lifetime)
	if err != nil {
		cancel()
		return nil, err
	}
	return &Client{link: link, recoverLink: dial, lifetime: lifetime, cancel: cancel}, nil
}

func lookupSaved(ctx context.Context, relayOrigin, lookupToken string) (mobilelink.ClientInvitation, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	room, err := mobilelink.LookupRoom(ctx, relayOrigin, lookupToken)
	// A freshly disconnected host needs a moment to publish its next room.
	// Only repeat a read of the relay directory. Authentication and every
	// application request remain single attempts.
	for _, delay := range []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second} {
		if !errors.Is(err, mobilelink.ErrNoInvitation) {
			break
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return room, ctx.Err()
		case <-timer.C:
		}
		room, err = mobilelink.LookupRoom(ctx, relayOrigin, lookupToken)
	}
	return room, err
}

type Response struct {
	Status      int
	ContentType string
	Body        []byte
}

// Request accepts only the mobile operation allowlist. Call off the main UI thread.
func (c *Client) Request(method, path string, body []byte) (*Response, error) {
	reply, err := c.Do(context.Background(), method, path, body)
	if err != nil {
		return nil, err
	}
	return &Response{reply.Status, reply.ContentType, reply.Body}, nil
}

// Do keeps the local WebView and pending draft alive across transport loss.
// Only a read may reopen the saved pairing. Never resend a mutation here.
func (c *Client) Do(ctx context.Context, method, path string, body []byte) (*mobilelink.Reply, error) {
	c.mu.Lock()
	link, closed := c.link, c.closed
	c.mu.Unlock()
	if closed {
		return nil, net.ErrClosed
	}
	reply, err := link.Do(ctx, method, path, body)
	if err == nil || method != http.MethodGet || c.recoverLink == nil || ctx.Err() != nil || !link.Disconnected() {
		return reply, err
	}
	result := c.recovery.DoChan("link", func() (any, error) {
		c.mu.Lock()
		current, closed := c.link, c.closed
		c.mu.Unlock()
		if closed {
			return nil, net.ErrClosed
		}
		if current != link {
			return current, nil
		}
		next, err := c.recoverLink(c.lifetime)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.closed {
			next.Close()
			return nil, net.ErrClosed
		}
		c.link = next
		link.Close()
		return next, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-result:
		if r.Err != nil {
			return nil, r.Err
		}
		return r.Val.(*mobilelink.Client).Do(ctx, method, path, body)
	}
}

// StartUI serves the app's shipped mobile.html and assets on an ephemeral
// IPv4 loopback port. Pass the one-use URL directly to a private WebView.
// The native shell must allow navigation only to this exact loopback origin
// and avoid logging the URL. Call Close when discarding this connection.
func (c *Client) StartUI(assetsDirectory string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.server != nil || !filepath.IsAbs(assetsDirectory) {
		return "", errors.New("UI needs an open connection, an absolute asset directory, and a single start")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", errors.New("cannot open local mobile UI listener")
	}
	browser, launchURL, err := mobilelink.NewBrowser("http://"+listener.Addr().String(), assetsDirectory, c)
	if err != nil {
		listener.Close()
		return "", err
	}
	server := &http.Server{Handler: browser, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 45 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16 << 10, ErrorLog: log.New(io.Discard, "", 0)}
	c.server, c.listener, c.browser = server, listener, browser
	go func() { _ = server.Serve(listener) }()
	return launchURL, nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.cancel != nil {
		c.cancel()
	}
	if c.server != nil {
		_ = c.server.Close()
		_ = c.listener.Close()
		_ = c.browser.Close()
	}
	return c.link.Close()
}
