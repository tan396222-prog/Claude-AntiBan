//go:build !windows

package workbench

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sylvinus/agent-vm/internal/guestos"
)

type disconnectedMacLoginResponse struct{ *httptest.ResponseRecorder }

func (disconnectedMacLoginResponse) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestMacOSMaintenanceHTTPBoundaryAndRunnerUpgrade(t *testing.T) {
	s := testServer(t)
	profile, err := os.MkdirTemp("/tmp", "cage-maint-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(profile)
	s.profile = profile
	s.macosGuest = &guestos.MacOSProfile{Instance: "mac", User: "trial"}
	s.ready = true
	if err := os.MkdirAll(s.instanceDir(), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(s.instanceDir(), "cage-login.sock"))
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan guestos.MacLoginRequest, 4)
	var legacy atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			var input guestos.MacLoginRequest
			if json.NewDecoder(connection).Decode(&input) == nil {
				requests <- input
				response := guestos.MacLoginReply{MaintenanceVersion: 1, Session: "fixture-session"}
				if legacy.Load() {
					response = guestos.MacLoginReply{Closed: true}
				}
				_ = json.NewEncoder(connection).Encode(response)
			}
			_ = connection.Close()
		}
	}()
	defer func() { listener.Close(); <-done }()
	post := func(path, body string, trusted bool) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, s.origin+path, strings.NewReader(body))
		request.Header.Set("Origin", s.origin)
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: s.cookieName(), Value: s.session})
		if trusted {
			request.AddCookie(&http.Cookie{Name: s.cookieName() + "-desktop", Value: trustedViewProof(s.token)})
		}
		response := httptest.NewRecorder()
		s.handler().ServeHTTP(response, request)
		return response
	}
	body := `{"action":"fill-password","maintenance":"projects","session":"fixture-session","geometry":"verified-window","password":"untrusted-input","initial_setup":true}`
	if response := post("/api/macos/maintenance", body, false); response.Code != http.StatusForbidden {
		t.Fatalf("untrusted maintenance accepted: %d", response.Code)
	}
	if response := post("/api/macos/maintenance", body, true); response.Code != http.StatusOK {
		t.Fatalf("trusted maintenance failed: %d %s", response.Code, response.Body.String())
	}
	input := <-requests
	if input.Setup || input.Password != "" || input.Maintenance != "projects" || input.Geometry != "verified-window" || input.Action != "fill-password" {
		t.Fatalf("maintenance boundary changed: action=%s purpose=%s setup=%v", input.Action, input.Maintenance, input.Setup)
	}
	for _, body := range []string{
		`{"action":"open","maintenance":"desktop"}`,
		`{"action":"show-password","maintenance":"projects"}`,
		`{"action":"finish","maintenance":"projects"}`,
	} {
		if response := post("/api/macos/maintenance", body, true); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid maintenance request returned %d", response.Code)
		}
	}
	if response := post("/api/macos/first-setup", `{"action":"open"}`, true); response.Code != http.StatusGone {
		t.Fatalf("completed initial setup was reopened: %d", response.Code)
	}
	legacy.Store(true)
	response := post("/api/macos/maintenance", `{"action":"open","maintenance":"projects"}`, true)
	<-requests
	var result guestos.MacLoginReply
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Recovery != "environment-restart" || !result.Closed {
		t.Fatalf("old runner did not request a safe restart: %d %s", response.Code, response.Body.String())
	}
	legacy.Store(false)
	if response := post("/api/macos/maintenance", `{"action":"open","maintenance":"projects"}`, true); response.Code != http.StatusOK {
		t.Fatal("could not open the current maintenance runner")
	}
	<-requests
	s.account.lock()
	select {
	case close := <-requests:
		if close.Action != "close" || close.Session != "fixture-session" || close.Maintenance != "projects" {
			t.Fatal("Cage lock did not revoke its maintenance session")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Cage lock retained the maintenance view")
	}

	// An open request can finish after the user locks and unlocks the workbench.
	// Its lease must belong to the account epoch that admitted that request.
	previousEpoch, _ := s.account.accessFor(true)
	s.account.mu.Lock()
	s.account.setKey(make([]byte, 32))
	s.account.mu.Unlock()
	s.trackMacLogin(previousEpoch,
		guestos.MacLoginRequest{Action: "open", Maintenance: "projects"},
		guestos.MacLoginReply{Session: "late-session"})
	select {
	case close := <-requests:
		if close.Action != "close" || close.Session != "late-session" {
			t.Fatal("a late open response attached to the new account epoch")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a late open response retained its cancelled maintenance view")
	}

	request := httptest.NewRequest(http.MethodPost, s.origin+"/api/macos/maintenance", strings.NewReader(`{"action":"open","maintenance":"projects"}`))
	request.Header.Set("Origin", s.origin)
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: s.cookieName(), Value: s.session})
	request.AddCookie(&http.Cookie{Name: s.cookieName() + "-desktop", Value: trustedViewProof(s.token)})
	s.handler().ServeHTTP(disconnectedMacLoginResponse{httptest.NewRecorder()}, request)
	if open := <-requests; open.Action != "open" {
		t.Fatal("disconnected response fixture did not open the maintenance view")
	}
	select {
	case close := <-requests:
		if close.Action != "close" || close.Session != "fixture-session" {
			t.Fatal("a failed HTTP reply did not close its newly opened view")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("an undelivered HTTP response retained its maintenance view")
	}
}
