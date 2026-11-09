package workbench

import (
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

func TestBrowserEventsShutdownDrainsIdleAndBlockedStreams(t *testing.T) {
	for _, slowReader := range []bool{false, true} {
		t.Run(fmt.Sprint("slow-reader=", slowReader), func(t *testing.T) {
			s := testServer(t)
			s.ready, s.credentials.Token = true, "guest-token"
			guestStarted, guestDone := make(chan struct{}), make(chan struct{})
			var sent atomic.Int64
			guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(guestDone)
				if r.URL.Path != "/events" || r.Header.Get("Authorization") != "Bearer guest-token" {
					t.Error("event stream lost destination or authentication")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				w.(http.Flusher).Flush()
				close(guestStarted)
				if !slowReader {
					<-r.Context().Done()
					return
				}
				frame := ": " + strings.Repeat("x", 64<<10) + "\n\n"
				for r.Context().Err() == nil {
					if _, err := io.WriteString(w, frame); err != nil {
						return
					}
					if err := http.NewResponseController(w).Flush(); err != nil {
						return
					}
					sent.Add(1)
				}
			}))
			defer guest.Close()
			s.browserControl = localBrowserClient(t, s.lifetimeContext(), guest)
			defer s.browserControl.close()
			handlerDone := make(chan struct{})
			handler := s.handler()
			app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(handlerDone)
				handler.ServeHTTP(w, r)
			}))
			defer app.Close()
			defer s.beginClose()
			conn, err := net.Dial("tcp", app.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if tcp, ok := conn.(*net.TCPConn); ok {
				_ = tcp.SetReadBuffer(1024)
			}
			// Leave the socket unread, including the response headers. The small
			// receive buffer makes sustained SSE output block in the HTTP writer.
			_, err = fmt.Fprintf(conn, "GET /api/browser/events HTTP/1.1\r\nHost: localhost:17777\r\nCookie: %s=%s\r\n\r\n", s.cookieName(), s.session)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-guestStarted:
			case <-time.After(3 * time.Second):
				t.Fatal("authenticated event stream did not reach the guest")
			}
			if slowReader {
				deadline := time.Now().Add(3 * time.Second)
				previous, stable := int64(0), 0
				for stable < 3 && time.Now().Before(deadline) {
					time.Sleep(20 * time.Millisecond)
					current := sent.Load()
					if current > 0 && current == previous {
						stable++
					} else {
						stable = 0
					}
					previous = current
				}
				if stable < 3 {
					t.Fatal("unread event stream did not apply backpressure")
				}
			}
			closed := make(chan error, 1)
			go func() { closed <- s.closeService() }()
			select {
			case err := <-closed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("service shutdown waited on a blocked event stream")
			}
			for name, done := range map[string]<-chan struct{}{"HTTP handler": handlerDone, "guest subscription": guestDone} {
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Fatalf("%s survived shutdown", name)
				}
			}
		})
	}
}
