package workbench

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type partialJSONBody struct {
	io.ReadCloser
	reads   int
	waiting chan struct{}
}

func (b *partialJSONBody) Read(data []byte) (int, error) {
	b.reads++
	if b.reads == 2 {
		close(b.waiting)
	}
	return b.ReadCloser.Read(data)
}

func TestCloseServiceInterruptsPartialJSONWithUpgradeHeader(t *testing.T) {
	s := testServer(t)
	waiting, finished := make(chan struct{}), make(chan struct{})
	handler := s.handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		r.Body = &partialJSONBody{ReadCloser: r.Body, waiting: waiting}
		handler.ServeHTTP(w, r)
	}))
	s.origin = server.URL
	conn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(); server.Close() }()
	_, err = fmt.Fprintf(conn, "POST /api/settings HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nCookie: %s=%s\r\nContent-Type: application/json\r\nContent-Length: 4096\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n{", strings.TrimPrefix(server.URL, "http://"), server.URL, s.cookieName(), s.session)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-waiting:
	case <-time.After(3 * time.Second):
		t.Fatal("ordinary settings handler did not wait for the remaining JSON body")
	}
	closed := make(chan error, 1)
	go func() { closed <- s.closeService() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("an Upgrade header bypassed cancellation of an ordinary HTTP body")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("service closed before the HTTP handler finished")
	}
}
