package mobilelink_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sylvinus/agent-vm/internal/mobilelink"
	"github.com/sylvinus/agent-vm/internal/mobilelink/nativeclient"
	"github.com/sylvinus/agent-vm/internal/relay"
	"github.com/sylvinus/agent-vm/internal/remotelink"
	"golang.org/x/net/http2"
)

type device struct{ key *ecdsa.PrivateKey }

func (d device) PublicKeyDER() ([]byte, error) { return x509.MarshalPKIXPublicKey(&d.key.PublicKey) }
func (d device) SignSHA256(hash []byte) ([]byte, error) {
	return ecdsa.SignASN1(rand.Reader, d.key, hash)
}

const session = "/api/mobile/cli/sessions/12345678-1234-4321-aaaa-123456789abc"

type fixture struct {
	base      string
	backend   *mobilelink.Workbench
	identity  *remotelink.Identity
	locked    atomic.Bool
	upstream  atomic.Int32
	activeGet chan struct{}
	finishGet chan struct{}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{activeGet: make(chan struct{}, 1), finishGet: make(chan struct{})}
	id, err := remotelink.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	f.identity = id
	ticket := strings.Repeat("test-ticket-", 5)
	const cookie = "local-cookie-must-stay-on-host"
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "http://"+r.Host {
			http.Error(w, "origin refused", 403)
			return
		}
		_, port, _ := net.SplitHostPort(r.Host)
		name := "cage_session_" + port
		if r.URL.Path == "/api/session" {
			var in struct{ Token string }
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&in) != nil || in.Token != ticket {
				http.Error(w, "ticket refused", 401)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: name, Value: cookie, Path: "/", HttpOnly: true})
			return
		}
		c, err := r.Cookie(name)
		if err != nil || c.Value != cookie || len(r.Cookies()) != 1 || r.Header.Get("Authorization") != "" {
			http.Error(w, "session refused", 401)
			return
		}
		f.upstream.Add(1)
		if f.locked.Load() {
			http.Error(w, "locked", 423)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/mobile/cli/projects":
			_, _ = io.WriteString(w, `{"projects":["B"]}`)
		case r.URL.Path == session && r.URL.RawQuery == "after=2":
			f.activeGet <- struct{}{}
			select {
			case <-f.finishGet:
				w.WriteHeader(204)
			case <-r.Context().Done():
			}
		case (r.URL.Path == session+"/messages" || r.URL.Path == session+"/approval") && r.Method == http.MethodPost:
			data, _ := io.ReadAll(r.Body)
			w.WriteHeader(201)
			_, _ = w.Write(data)
		case r.URL.Path == "/api/mobile/cli/sessions":
			http.Redirect(w, r, "/api/account", 307)
		default:
			t.Errorf("unexpected local upstream operation: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(control.Close)
	f.backend, err = mobilelink.OpenWorkbench(context.Background(), control.URL, ticket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.backend.Close)
	r, err := relay.New(relay.Config{AdminToken: strings.Repeat("t", 64)})
	if err != nil {
		t.Fatal(err)
	}
	rs := httptest.NewServer(r)
	t.Cleanup(rs.Close)
	t.Cleanup(func() { _ = r.Close() })
	f.base = rs.URL
	return f
}

func (f *fixture) startHost(t *testing.T, pin string) relay.Invitation {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	room, err := mobilelink.CreateRoom(ctx, f.base, strings.Repeat("t", 64))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- mobilelink.ServeHost(ctx, f.base, room.ID, room.HostToken, f.identity, pin, f.backend) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("host bridge did not stop")
		}
	})
	return room
}

func TestNativeRequestsAcrossRealRelay(t *testing.T) {
	f := newFixture(t)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	d := device{key}
	pin, err := nativeclient.Fingerprint(d)
	if err != nil {
		t.Fatal(err)
	}
	room := f.startHost(t, pin)
	c, err := nativeclient.Open(f.base, room.ID, room.ClientToken, f.identity.Pin, d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	p, err := c.Request("GET", "/api/mobile/cli/projects", nil)
	if err != nil || p.Status != 200 || p.ContentType != "application/json" || !bytes.Contains(p.Body, []byte("B")) {
		t.Fatalf("project read failed: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := c.Request("GET", session+"?after=2", nil)
		done <- err
	}()
	select {
	case <-f.activeGet:
	case <-time.After(3 * time.Second):
		t.Fatal("revision read did not reach host")
	}
	message := []byte(`{"text":"在项目 B 运行测试"}`)
	p, err = c.Request("POST", session+"/messages", message)
	close(f.finishGet)
	if err != nil || p.Status != 201 || !bytes.Equal(p.Body, message) {
		t.Fatalf("concurrent message submission failed: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	decision := []byte(`{"request_id":"12345678-1234-4321-aaaa-123456789abc","approval_id":"12345678-1234-4321-aaaa-123456789abd","allow":false}`)
	p, err = c.Request("POST", session+"/approval", decision)
	if err != nil || p.Status != 201 || !bytes.Equal(p.Body, decision) {
		t.Fatalf("approval reply failed: %v", err)
	}
	before := f.upstream.Load()
	for _, path := range []string{"/api/account", "/api/launch", "/api/mobile/cli/sessions/../projects", session + "/%73top", session + "?after=1&after=2", session + "?after=-1", "http://localhost/api/mobile/cli/projects", "//localhost/api/mobile/cli/projects"} {
		if _, err := c.Request("GET", path, nil); err == nil {
			t.Errorf("unsafe operation accepted: %s", path)
		}
	}
	if f.upstream.Load() != before {
		t.Fatal("refused operation reached host")
	}
	p, err = c.Request("GET", "/api/mobile/cli/sessions", nil)
	if err != nil || p.Status != 502 || f.upstream.Load() != before+1 {
		t.Fatal("host redirect was followed or exposed")
	}
	f.locked.Store(true)
	p, err = c.Request("GET", "/api/mobile/cli/projects", nil)
	if err != nil || p.Status != 423 {
		t.Fatalf("account lock lost at bridge: %v", err)
	}
	assets := t.TempDir()
	if err := os.WriteFile(filepath.Join(assets, "mobile.html"), []byte("mobile"), 0600); err != nil {
		t.Fatal(err)
	}
	entry, err := c.StartUI(assets)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(entry)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Path != "/link-bootstrap" || u.Fragment == "" {
		t.Fatal("invalid native UI entry")
	}
	browser := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 2 * time.Second}
	defer browser.CloseIdleConnections()
	res, err := browser.Get(u.Scheme + "://" + u.Host + u.Path)
	if err != nil {
		t.Fatal("cannot open native UI bootstrap")
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("native UI bootstrap unavailable")
	}
	if _, err := c.StartUI(assets); err == nil {
		t.Fatal("native UI opened twice")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if res, err := browser.Get(u.Scheme + "://" + u.Host + u.Path); err == nil {
		res.Body.Close()
		t.Fatal("discarded native UI remained reachable")
	}
}

// A valid but independently implemented device can put arbitrary HTTP/2
// messages onto the link; the host enforces the contract independently.
func TestPairedPeerCannotBypassHostFilter(t *testing.T) {
	f := newFixture(t)
	key, err := remotelink.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	room := f.startHost(t, key.Pin)
	dial := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	ws, _, err := dial.Dial("ws"+strings.TrimPrefix(f.base, "http")+"/v1/rooms/"+room.ID+"/client", http.Header{"Authorization": {"Bearer " + room.ClientToken}})
	if err != nil {
		t.Fatal("test client did not reach relay")
	}
	conn, err := remotelink.Client(context.Background(), ws, key, f.identity.Pin)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.NetConn().Close()
	cc, err := (&http2.Transport{}).NewClientConn(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	for _, tc := range []struct {
		method, path, host, body string
		want                     int
	}{
		{"POST", "/api/launch", "cage-mobile.invalid", "{}", 404},
		{"GET", "/api/mobile/cli/sessions/../projects", "cage-mobile.invalid", "", 404},
		{"GET", session + "?after=1&after=2", "cage-mobile.invalid", "", 404},
		{"GET", "/api/mobile/cli/projects", "other-host.invalid", "", 403},
		{"POST", session + "/messages", "cage-mobile.invalid", strings.Repeat("x", (72<<10)+1), 413},
		{"GET", "/api/mobile/cli/projects", "cage-mobile.invalid", "", 200},
	} {
		req, err := http.NewRequest(tc.method, "https://"+tc.host+tc.path, strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer forged")
		req.Header.Set("Cookie", "other=forged")
		req.Header.Set("Origin", "https://foreign.invalid")
		req.Header.Set("Content-Type", "application/json")
		res, err := cc.RoundTrip(req)
		if err != nil {
			t.Fatalf("unexpected mobile protocol failure: %v", err)
		}
		res.Body.Close()
		if res.StatusCode != tc.want {
			t.Fatalf("raw %s status=%d, want %d", tc.path, res.StatusCode, tc.want)
		}
	}
	if calls := f.upstream.Load(); calls != 1 {
		t.Fatalf("host accepted %d upstream requests, want 1", calls)
	}
}

func TestLocalBrowserSessionAndHostHeaders(t *testing.T) {
	f := newFixture(t)
	id, err := remotelink.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	room := f.startHost(t, id.Pin)
	client, err := mobilelink.ConnectClient(context.Background(), f.base, room.ID, room.ClientToken, id, f.identity.Pin)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mobile.html"), []byte("Cage mobile test"), 0600); err != nil {
		t.Fatal(err)
	}
	web := httptest.NewUnstartedServer(nil)
	origin := "http://" + web.Listener.Addr().String()
	b, launch, err := mobilelink.NewBrowser(origin, dir, client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	web.Config.Handler = b
	web.Start()
	t.Cleanup(web.Close)
	entry, _ := url.Parse(launch)
	body, _ := json.Marshal(map[string]string{"token": entry.Fragment})
	call := func(method, path, ref string, cookie *http.Cookie, data []byte) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, origin+path, bytes.NewReader(data))
		if ref != "" {
			req.Header.Set("Origin", ref)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer untrusted-browser-value")
		if cookie != nil {
			req.AddCookie(cookie)
			req.AddCookie(&http.Cookie{Name: "cage_session_host", Value: "untrusted-browser-value"})
		}
		res, e := web.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { res.Body.Close() })
		return res
	}
	if res := call("GET", "/", "", nil, nil); res.StatusCode != 401 {
		t.Fatal("unauthenticated browser obtained UI")
	}
	if res := call("GET", "/link-bootstrap?page=archived", "", nil, nil); res.StatusCode != 200 {
		t.Fatal("native archive entry could not bootstrap")
	}
	if res := call("GET", "/link-bootstrap?page=other", "", nil, nil); res.StatusCode != 401 {
		t.Fatal("unexpected native bootstrap query accepted")
	}
	if res := call("POST", "/link-session", "https://untrusted.invalid", nil, body); res.StatusCode != 403 {
		t.Fatal("cross-origin browser obtained session")
	}
	res := call("POST", "/link-session", origin, nil, body)
	cookies := res.Cookies()
	if res.StatusCode != 204 || len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("browser bootstrap did not issue a protected local cookie")
	}
	if res := call("POST", "/link-session", origin, nil, body); res.StatusCode != 401 {
		t.Fatal("bootstrap was usable twice")
	}
	for _, path := range []string{"/", "/?page=archived", "/api/mobile/cli/projects"} {
		res = call("GET", path, "", cookies[0], nil)
		if res.StatusCode != 200 || len(res.Cookies()) != 0 {
			t.Fatalf("local browser route failed or leaked cookie: %s", path)
		}
	}
	if res := call("POST", session+"/messages", "https://untrusted.invalid", cookies[0], []byte("{}")); res.StatusCode != 403 {
		t.Fatal("foreign origin used authenticated bridge")
	}
	if res := call("POST", "/api/account/unlock", origin, cookies[0], []byte("{}")); res.StatusCode != 404 {
		t.Fatal("non-mobile route exposed to browser")
	}
}

func TestSavedNativeClientUsesNewRoomsAndChecksHostIdentity(t *testing.T) {
	f := newFixture(t)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	d := device{key}
	pin, err := nativeclient.Fingerprint(d)
	if err != nil {
		t.Fatal(err)
	}
	lookup := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	wrong := "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA"
	if _, err := mobilelink.CreateDiscoverableRoom(context.Background(), f.base, strings.Repeat("x", 64), lookup); err == nil || mobilelink.Retryable(err) {
		t.Fatal("invalid admin credentials must stop the host service")
	}
	if _, err := mobilelink.LookupRoom(context.Background(), f.base, wrong); err == nil {
		t.Fatal("unregistered lookup token accepted")
	}
	other, err := remotelink.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	previous := ""
	for _, expectedPin := range []string{other.Pin, f.identity.Pin, f.identity.Pin} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		inv, err := mobilelink.CreateDiscoverableRoom(ctx, f.base, strings.Repeat("t", 64), lookup)
		// The host connection can finish before the relay retires its room.
		// Saved hosts back off on this transient conflict too; the occupied-room
		// assertion below must still reject displacement without retrying.
		for previous != "" && err != nil && mobilelink.Retryable(err) && ctx.Err() == nil {
			time.Sleep(10 * time.Millisecond)
			inv, err = mobilelink.CreateDiscoverableRoom(ctx, f.base, strings.Repeat("t", 64), lookup)
		}
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if inv.ID == previous {
			cancel()
			t.Fatal("room reused after disconnect")
		}
		if _, err := mobilelink.CreateDiscoverableRoom(ctx, f.base, strings.Repeat("t", 64), lookup); err == nil || !mobilelink.Retryable(err) {
			cancel()
			t.Fatal("an occupied saved room must remain intact and back off")
		}
		previous = inv.ID
		done := make(chan error, 1)
		go func() { done <- mobilelink.ServeHost(ctx, f.base, inv.ID, inv.HostToken, f.identity, pin, f.backend) }()
		for {
			visible, err := mobilelink.LookupRoom(ctx, f.base, lookup)
			if err == nil && visible.ID == inv.ID {
				break
			}
			if ctx.Err() != nil {
				cancel()
				t.Fatal("registered host never became discoverable")
			}
			time.Sleep(10 * time.Millisecond)
		}
		if _, err := mobilelink.LookupRoom(ctx, f.base, wrong); err == nil {
			cancel()
			t.Fatal("incorrect lookup token revealed ready room")
		}
		c, err := nativeclient.OpenSaved(f.base, lookup, expectedPin, d)
		if expectedPin != f.identity.Pin {
			if err == nil {
				c.Close()
				cancel()
				t.Fatal("saved client accepted the wrong host fingerprint")
			}
		} else {
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			resp, err := c.Request("GET", "/api/mobile/cli/projects", nil)
			if err != nil || resp.Status != 200 {
				c.Close()
				cancel()
				t.Fatal("saved native operation failed")
			}
			_ = c.Close()
		}
		select {
		case <-done:
		case <-ctx.Done():
			cancel()
			t.Fatal("used room did not close")
		}
		cancel()
	}
}

// A phone may have a pending approval open when its network changes. Reading
// through the same saved client must recover the transport without replaying it.
func TestSavedClientRecoversReadAfterHostDisconnect(t *testing.T) {
	f := newFixture(t)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	d := device{key}
	pin, err := nativeclient.Fingerprint(d)
	if err != nil {
		t.Fatal(err)
	}
	lookup := strings.Repeat("A", 43)
	start := func(reconnecting bool) func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		room, err := mobilelink.CreateDiscoverableRoom(ctx, f.base, strings.Repeat("t", 64), lookup)
		// Transport shutdown reaches the relay asynchronously. Match the saved
		// host's retry policy while the previous room finishes retiring.
		for reconnecting && err != nil && mobilelink.Retryable(err) && ctx.Err() == nil {
			time.Sleep(10 * time.Millisecond)
			room, err = mobilelink.CreateDiscoverableRoom(ctx, f.base, strings.Repeat("t", 64), lookup)
		}
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = mobilelink.ServeHost(ctx, f.base, room.ID, room.HostToken, f.identity, pin, f.backend)
		}()
		return func() { cancel(); <-done }
	}
	drop := start(false)
	c, err := nativeclient.OpenSaved(f.base, lookup, f.identity.Pin, d)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Request("GET", "/api/mobile/cli/projects", nil); err != nil {
		t.Fatal(err)
	}
	drop()
	stop := start(true)
	defer stop()

	calls := f.upstream.Load()
	if _, err := c.Request("POST", session+"/approval", []byte(`{"allow":true}`)); err == nil {
		t.Fatal("a pending approval silently crossed into the replacement connection")
	}
	if f.upstream.Load() != calls {
		t.Fatal("approval reached the host")
	}
	results := make(chan error, 2)
	for range 2 {
		go func() {
			reply, err := c.Request("GET", "/api/mobile/cli/projects", nil)
			if err == nil && reply.Status != http.StatusOK {
				err = fmt.Errorf("unexpected status %d", reply.Status)
			}
			results <- err
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if got := f.upstream.Load() - calls; got != 2 {
		t.Fatalf("expected only two recovered reads, got %d requests", got)
	}
}
