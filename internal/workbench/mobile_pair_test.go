package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sylvinus/agent-vm/internal/mobilelink"
	"github.com/sylvinus/agent-vm/internal/relay"
	"github.com/sylvinus/agent-vm/internal/remotelink"
)

// Drive the same management routes as the desktop settings page. Separate
// workbench instances must recover the same on-disk pairing after a restart.
func TestDesktopPhonePairingLifecycle(t *testing.T) {
	edge, err := relay.New(relay.Config{PublicRooms: true, AdminToken: strings.Repeat("x", 32)})
	if err != nil {
		t.Fatal(err)
	}
	relayHTTP := httptest.NewServer(edge)
	t.Cleanup(func() { edge.Close(); relayHTTP.Close() })
	origin := relayHTTP.URL
	s, networkGate := grantedToolUpdateServer(t)
	serve := func(s *Server) {
		server := httptest.NewUnstartedServer(s.handler())
		s.origin = "http://" + server.Listener.Addr().String()
		server.Start()
		t.Cleanup(server.Close)
		t.Cleanup(s.stopPhone)
	}
	manage := func(s *Server, method string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, s.origin+"/api/mobile-pair", strings.NewReader("{}"))
		r.Header.Set("Origin", s.origin)
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: s.cookieName(), Value: s.session})
		w := httptest.NewRecorder()
		s.handler().ServeHTTP(w, r)
		return w
	}
	s.preferences.MobileRelay = origin
	serve(s)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	phone, err := remotelink.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	w := manage(s, "POST")
	var code struct{ Manual, Image string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &code) != nil || !strings.HasPrefix(code.Manual, "CAGE-") || !strings.HasPrefix(code.Image, "data:image/png;base64,") {
		t.Fatalf("desktop pairing code unavailable (HTTP %d)", w.Code)
	}
	credentials, err := mobilelink.RedeemPair(ctx, code.Manual, origin, phone.Pin)
	if err != nil {
		t.Fatal(err)
	}
	var record mobilelink.PairedHost
	if err := json.Unmarshal([]byte(credentials), &record); err != nil {
		t.Fatal(err)
	}
	connect := func() *mobilelink.Client {
		t.Helper()
		for {
			room, err := mobilelink.LookupRoom(ctx, origin, record.Lookup)
			if err == nil {
				client, err := mobilelink.ConnectClient(ctx, origin, room.ID, room.ClientToken, phone, record.HostPin)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { client.Close() })
				return client
			}
			if !errors.Is(err, mobilelink.ErrNoInvitation) || ctx.Err() != nil {
				t.Fatal(err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	read := func(client *mobilelink.Client, status int) {
		t.Helper()
		reply, err := client.Do(ctx, "GET", "/api/mobile/cli/projects", nil)
		if err != nil {
			t.Fatal(err)
		}
		if reply.Status != status {
			t.Fatalf("projects: got %d, want %d", reply.Status, status)
		}
	}
	client := connect()
	read(client, 200)
	// Legacy pairings gain a model over the authenticated phone tunnel without
	// changing their key, lookup token, or the optical pairing protocol.
	if w := manage(s, "GET"); w.Code != 200 || !strings.Contains(w.Body.String(), `"device":null`) {
		t.Fatal("legacy pairing should have no invented model")
	}
	reply, err := client.Do(ctx, "POST", "/api/mobile/device", []byte(`{"platform":"android","model":"Google Pixel 9 Pro"}`))
	if err != nil || reply.Status != http.StatusOK {
		t.Fatalf("device report failed: %+v, %v", reply, err)
	}
	if w := manage(s, "GET"); w.Code != 200 || !strings.Contains(w.Body.String(), `"model":"Google Pixel 9 Pro"`) || strings.Contains(w.Body.String(), record.Lookup) || strings.Contains(w.Body.String(), phone.Pin) {
		t.Fatal("pairing status must expose model without credentials")
	}
	for _, input := range []string{`{"platform":"other","model":"Unknown"}`, `{"platform":"ios","model":""}`, `{"platform":"ios","model":"bad\nmodel"}`} {
		reply, err := client.Do(ctx, "POST", "/api/mobile/device", []byte(input))
		if err != nil || reply.Status != http.StatusBadRequest {
			t.Fatalf("invalid device report was accepted: %+v, %v", reply, err)
		}
	}
	// An authenticated desktop session is not the pinned phone tunnel.
	r := httptest.NewRequest("POST", s.origin+"/api/mobile/device", strings.NewReader(`{"platform":"ios","model":"other phone"}`))
	r.Header.Set("Origin", s.origin)
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: s.cookieName(), Value: s.session})
	w = httptest.NewRecorder()
	s.handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("desktop session could overwrite phone model: %d", w.Code)
	}
	testSharedPhoneFavorites(t, ctx, s, client)
	testPairedBrowser(t, ctx, s, client, networkGate)
	read(client, 423)
	client.Close()
	s.phones.mu.Lock()
	powerCtx, powerDone := s.phones.powerCtx, s.phones.powerDone
	s.phones.mu.Unlock()
	if powerCtx == nil || powerCtx.Err() != nil {
		t.Fatal("phone disconnect stopped standby power request")
	}
	s.stopPhone()
	select {
	case <-powerDone:
	default:
		t.Fatal("host shutdown left power request running")
	}
	// Restore an old enrollment whose Desktop and browser switches were off.
	saved, err := os.ReadFile(s.phoneFile())
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]json.RawMessage
	if err := json.Unmarshal(saved, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy["BrowserAllowed"] = json.RawMessage("false")
	legacy["DesktopAllowed"] = json.RawMessage("false")
	newHost, restoredGate := grantedToolUpdateServer(t)
	// This restored host uses a fake VM and guest frames, so no real X display
	// exists. Its display effect has the same boolean contract as the first host.
	newHost.desktopDisplayUpdate = func(bool) error { return nil }
	if err := writePrivate(newHost.phoneFile(), legacy); err != nil {
		t.Fatal(err)
	}
	serve(newHost)
	if err := newHost.restorePhone(); err != nil {
		t.Fatal(err)
	}
	if w := manage(newHost, "GET"); w.Code != 200 || !strings.Contains(w.Body.String(), `"model":"Google Pixel 9 Pro"`) {
		t.Fatal("phone model was lost after restarting the host")
	}
	client = connect()
	read(client, 200)
	newHost.browserControl = browserClientForTest(t, newHost, browserTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/phone-browser/action" {
			restoredGate.Close("network lost before action frame returned")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}")), Header: http.Header{}}, nil
	}))
	for _, surface := range []string{"browser", "desktop"} {
		reply, err := client.Do(ctx, "POST", "/api/mobile/"+surface+"/open", []byte(`{"id":"d872e7ef-e0ef-44b7-a256-38395cc6ee16","width":390,"height":640}`))
		if err != nil || reply.Status != 200 {
			t.Fatalf("restored %s session: %+v, %v", surface, reply, err)
		}
	}
	reply, err = client.Do(ctx, "POST", "/api/mobile/browser/d872e7ef-e0ef-44b7-a256-38395cc6ee16/action",
		[]byte(`{"type":"scroll","document":"test","x":0,"y":0,"dx":0,"dy":20,"return_frame":true}`))
	if err != nil || reply.Status != http.StatusConflict {
		t.Fatalf("network change must withhold attached frame: %+v, %v", reply, err)
	}
	if w := manage(newHost, "DELETE"); w.Code != 200 {
		t.Fatalf("revoke: HTTP %d", w.Code)
	}
	newHost.phones.mu.Lock()
	removedPowerDone := newHost.phones.powerDone
	newHost.phones.mu.Unlock()
	select {
	case <-removedPowerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("unpairing left power request running")
	}
	if _, err := os.Stat(newHost.phoneFile()); !os.IsNotExist(err) {
		t.Fatal("revoked credentials still on disk")
	}
	if w := manage(newHost, "GET"); !strings.Contains(w.Body.String(), `"device":null`) || strings.Contains(w.Body.String(), "Pixel") {
		t.Fatal("removed phone model remains in pairing status")
	}
	for {
		_, err := client.Do(ctx, "GET", "/api/mobile/cli/projects", nil)
		if err != nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("active phone retained access after revocation")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := mobilelink.LookupRoom(ctx, origin, record.Lookup); !errors.Is(err, mobilelink.ErrNoInvitation) {
		t.Fatalf("revoked lookup %v", err)
	}
}
