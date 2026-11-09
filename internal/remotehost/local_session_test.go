package remotehost

import (
	"context"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func TestLocalSessionMarksRefusedControllerLostButAllowsRequestCancellation(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://localhost:" + strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	u, _ := url.Parse(origin)
	jar, _ := cookiejar.New(nil)
	jar.SetCookies(u, []*http.Cookie{{Name: "private", Value: "test", Path: "/"}})
	s, err := LocalSession(context.Background(), origin, PublicManifest{}, jar)
	if err != nil {
		l.Close()
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", origin+"/api/account", nil)
	if _, err = s.Client.Do(req); err == nil || s.Context.Err() != nil {
		l.Close()
		t.Fatal("cancelled request invalidated view", err)
	}
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Client.Get(origin + "/api/account"); err == nil {
		t.Fatal("closed controller accepted request")
	}
	select {
	case <-s.Context.Done():
	case <-time.After(time.Second):
		t.Fatal("closed controller retained a ready view")
	}
}
