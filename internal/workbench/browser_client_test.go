package workbench

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func browserClientForTest(t *testing.T, s *Server, transport http.RoundTripper) *browserClient {
	t.Helper()
	client := newBrowserClient(s.lifetimeContext())
	client.http.Transport = transport
	t.Cleanup(client.close)
	return client
}

// Keep the production URL, authentication and redirect policy while routing
// the socket to a private HTTP server. No request reaches the real guest port.
func localBrowserClient(t *testing.T, parent context.Context, server *httptest.Server) *browserClient {
	t.Helper()
	client := newBrowserClient(parent)
	client.http.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "127.0.0.1:18500" {
			return nil, fmt.Errorf("unexpected browser address: %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	t.Cleanup(client.close)
	return client
}

func TestBrowserClientBoundsCompleteResponseAndRefusesRedirect(t *testing.T) {
	var redirected atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Store(true) }))
	defer target.Close()
	guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "127.0.0.1:18500" || r.Header.Get("Authorization") != "Bearer "+r.URL.Query().Get("token") {
			t.Error("browser destination or per-request credentials changed")
		}
		switch r.URL.Path {
		case "/exact":
			_, _ = io.WriteString(w, strings.Repeat("x", browserResponseLimit))
		case "/large":
			_, _ = io.WriteString(w, strings.Repeat("x", browserResponseLimit+1))
		case "/redirect":
			http.Redirect(w, r, target.URL, http.StatusFound)
		}
	}))
	defer guest.Close()
	client := localBrowserClient(t, context.Background(), guest)
	data, err := client.request(context.Background(), "first", http.MethodGet, "/exact?token=first", nil)
	if err != nil || len(data) != browserResponseLimit {
		t.Fatalf("exact response limit rejected: %d %v", len(data), err)
	}
	if data, err := client.request(context.Background(), "second", http.MethodGet, "/large?token=second", nil); err == nil || data != nil {
		t.Fatal("oversized response was silently truncated")
	}
	if _, err := client.request(context.Background(), "third", http.MethodGet, "/redirect?token=third", nil); err == nil || redirected.Load() {
		t.Fatal("browser redirect was followed")
	}
}

func TestBrowserClientCloseCancelsRequestsAndEvents(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint("events=", stream), func(t *testing.T) {
			started, guestDone := make(chan struct{}), make(chan struct{})
			guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, ": ready\n\n")
				w.(http.Flusher).Flush()
				close(started)
				<-r.Context().Done()
				close(guestDone)
			}))
			defer guest.Close()
			client := localBrowserClient(t, context.Background(), guest)
			defer client.close()
			done := make(chan error, 1)
			go func() {
				if stream {
					body, err := client.events(context.Background(), "token")
					if err == nil {
						_, err = io.ReadAll(body)
						body.Close()
					}
					done <- err
				} else {
					_, err := client.request(context.Background(), "token", http.MethodGet, "/tabs", nil)
					done <- err
				}
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("guest request did not start")
			}
			client.close()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("cancelled response appeared complete")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("browser close did not release in-flight response")
			}
			select {
			case <-guestDone:
			case <-time.After(3 * time.Second):
				t.Fatal("guest subscription survived browser close")
			}
			if _, err := client.events(context.Background(), "later-token"); !errors.Is(err, context.Canceled) {
				t.Fatalf("closed browser accepted new subscription: %v", err)
			}
		})
	}
}

func TestBrowserSubscriptionCancellationKeepsClientUsable(t *testing.T) {
	guestDone := make(chan struct{})
	guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			_, _ = io.WriteString(w, `{"ok":true}`)
			return
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(guestDone)
	}))
	defer guest.Close()
	lifetime, closeLifetime := context.WithCancel(context.Background())
	defer closeLifetime()
	client := localBrowserClient(t, lifetime, guest)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body, err := client.events(ctx, "token")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	cancel()
	select {
	case <-guestDone:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled subscription stayed connected")
	}
	if _, err := client.request(context.Background(), "next-token", http.MethodGet, "/health", nil); err != nil {
		t.Fatalf("closing a subscription closed the browser client: %v", err)
	}
	closeLifetime()
	if _, err := client.events(context.Background(), "token"); !errors.Is(err, context.Canceled) {
		t.Fatalf("browser accepted request after service lifetime ended: %v", err)
	}
}
