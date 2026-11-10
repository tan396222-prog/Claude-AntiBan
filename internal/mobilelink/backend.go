package mobilelink

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Workbench struct {
	phoneRoom string
	origin    string
	jar       http.CookieJar
	transport *http.Transport
}

func loopback(host string) bool {
	ip := net.ParseIP(host)
	return host == "localhost" || ip != nil && ip.IsLoopback()
}

func explicitPort(u *url.URL) bool {
	port, err := strconv.Atoi(u.Port())
	return err == nil && port >= 1 && port <= 65535
}

// OpenWorkbench consumes a ticket issued by the trusted local cage server-entry
// command. Only the host process ever sees the ticket and the resulting cookie.
func OpenWorkbench(ctx context.Context, origin, ticket string) (*Workbench, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || !loopback(u.Hostname()) || !explicitPort(u) ||
		u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || len(ticket) < 32 || len(ticket) > 512 {
		return nil, errors.New("invalid local Cage entry")
	}
	hostname := u.Hostname()
	if hostname == "localhost" {
		hostname = "127.0.0.1"
	}
	target := net.JoinHostPort(hostname, u.Port())
	dialer := net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{Proxy: nil, MaxIdleConnsPerHost: 8, IdleConnTimeout: time.Minute, ResponseHeaderTimeout: 35 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if network != "tcp" || address != u.Host {
				return nil, errors.New("nonlocal upstream refused")
			}
			return dialer.DialContext(ctx, "tcp", target)
		},
	}
	jar, _ := cookiejar.New(nil)
	b := &Workbench{origin: origin, transport: transport, jar: jar}
	payload, err := json.Marshal(map[string]string{"token": ticket})
	if err != nil {
		return nil, errors.New("invalid entry ticket")
	}
	defer clear(payload)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/api/session", bytes.NewReader(payload))
	if err != nil {
		return nil, errors.New("invalid local Cage origin")
	}
	req.GetBody = nil
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	response, err := transport.RoundTrip(req)
	if err != nil {
		b.Close()
		return nil, errors.New("Cage entry ticket exchange failed")
	}
	response.Body.Close()
	if response.StatusCode == http.StatusOK {
		for _, cookie := range response.Cookies() {
			if cookie.Name == "cage_session_"+u.Port() && cookie.Value != "" {
				jar.SetCookies(u, []*http.Cookie{cookie})
			}
		}
	}
	if len(jar.Cookies(u)) != 1 {
		b.Close()
		return nil, errors.New("Cage did not issue a valid local session cookie")
	}
	return b, nil
}

// SetPhoneRoom binds graphical and device metadata requests to this authenticated, UI-managed
// pairing. The room proof is never accepted from the phone or sent to it.
func (b *Workbench) SetPhoneRoom(proof string) { b.phoneRoom = proof }

const PhoneRoomHeader = "X-Cage-Phone-Room"

func (b *Workbench) Close() { b.transport.CloseIdleConnections() }

// RoundTrip rebuilds requests in operations. This private transport receives
// no caller-supplied headers and never follows redirects.
type localTransport struct{ b *Workbench }

func (t localTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.b.phoneRoom != "" && strings.HasPrefix(req.URL.Path, "/api/mobile/") {
		req.Header.Set(PhoneRoomHeader, t.b.phoneRoom)
	}
	for _, cookie := range t.b.jar.Cookies(req.URL) {
		req.AddCookie(cookie)
	}
	return t.b.transport.RoundTrip(req)
}

func (b *Workbench) handler() http.Handler {
	return operations(func(ctx context.Context, method, path string, body []byte) (*Reply, error) {
		return request(ctx, localTransport{b}, b.origin, method, path, body, true)
	})
}
