package workbench

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/guestos"
)

func initializationRequest(s *Server, action string, trusted bool) *httptest.ResponseRecorder {
	method, body := http.MethodGet, ""
	if action != "" {
		method, body = http.MethodPost, `{"action":"`+action+`"}`
	}
	r := httptest.NewRequest(method, s.origin+"/api/environment/initialization", strings.NewReader(body))
	r.Header.Set("Origin", s.origin)
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: s.cookieName(), Value: s.session})
	if trusted {
		r.AddCookie(&http.Cookie{Name: s.cookieName() + "-desktop", Value: trustedViewProof(s.token)})
	}
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, r)
	return w
}

func TestInitializationPersistsBeforeDiskCreationAndDoesNotResetExisting(t *testing.T) {
	s := testServer(t)
	read := func() initializationView {
		t.Helper()
		w := initializationRequest(s, "", true)
		var view initializationView
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil {
			t.Fatalf("initialization: %d %s", w.Code, w.Body.String())
		}
		return view
	}
	if !read().Required {
		t.Fatal("fresh profile bypassed initialization")
	}
	for _, action := range []string{"begin", "install"} {
		if w := initializationRequest(s, action, true); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if err := os.MkdirAll(filepath.Join(s.profile, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.profile, "state", ".agent-vm-base-version"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	view := read()
	if !view.Required || !view.Started || !view.Tracked {
		t.Fatalf("partial installation bypassed durable state: %+v", view)
	}
	// A new controller sees the same state, even after creating its base disk.
	restarted := testServer(t)
	restarted.profile = s.profile
	if next, err := restarted.initializationState(); err != nil || !next.Required || !next.Started {
		t.Fatalf("restart: %+v %v", next, err)
	}
	if err := os.Remove(filepath.Join(s.profile, "initialization.json")); err != nil {
		t.Fatal(err)
	}
	if read().Required {
		t.Fatal("existing Linux installation forced to reinstall")
	}
	s.windowsGuest = &guestos.WindowsProfile{}
	if next, err := s.initializationState(); err != nil || next.Required {
		t.Fatalf("existing Windows installation: %+v %v", next, err)
	}
	s.windowsGuest = nil
	s.macosGuest = &guestos.MacOSProfile{}
	if next, err := s.initializationState(); err != nil || next.Required {
		t.Fatalf("existing Mac installation: %+v %v", next, err)
	}
}

func TestInitializationCompletionRequiresReadyGuestAndCurrentNetworkEvidence(t *testing.T) {
	s := testServer(t)
	if w := initializationRequest(s, "begin", false); w.Code != http.StatusForbidden {
		t.Fatal("untrusted initialization accepted", w.Code)
	}
	if w := initializationRequest(s, "install", true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := initializationRequest(s, "complete", true); w.Code == 200 {
		t.Fatal("unready guest accepted")
	}
	s.windowsGuest = &guestos.WindowsProfile{}
	s.windowsState.Running = true
	s.ready = true
	if w := initializationRequest(s, "complete", true); w.Code == 200 {
		t.Fatal("missing network report accepted")
	}
	cfg, err := cage.Load(s.configPath)
	if err != nil {
		t.Fatal(err)
	}
	report := cage.SafetyReport{Policy: cage.SafetyPolicyVersion, ID: "initialization", RunID: "test-vm", CheckID: "test-check", Configuration: cfg.Fingerprint(), CreatedAt: time.Now()}
	for _, id := range []string{"egress_policy", "proxy_transport", "dns_transport", "guest_settings", "browser_settings", "filesystem_boundary", "management_boundary", "fail_closed", "udp_policy", "ipv6_policy", "exit_consistency"} {
		report.Checks = append(report.Checks, cage.SafetyCheck{ID: id, Critical: true, State: cage.CheckPassed, Evidence: "isolated test", CheckedAt: report.CreatedAt})
	}
	s.safety.Gate = &cage.GateStatus{Mode: cage.GateOpen, PolicyVerified: true, RunID: report.RunID, Configuration: cfg.Fingerprint(), Until: time.Now().Add(time.Minute), Report: &report}
	s.setJobForTest(Job{Running: true, Kind: "start"})
	if w := initializationRequest(s, "complete", true); w.Code == 200 {
		t.Fatal("unfinished job accepted")
	}
	s.setJobForTest(Job{})
	s.safety.Gate.Configuration = "another-config"
	if w := initializationRequest(s, "complete", true); w.Code == 200 {
		t.Fatal("stale configuration accepted")
	}
	s.safety.Gate.Configuration = cfg.Fingerprint()
	if w := initializationRequest(s, "complete", true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	s.ready = false
	s.windowsState.Running = false
	if view, err := s.initializationState(); err != nil || view.Required {
		t.Fatalf("completed stopped environment lost initialization: %+v %v", view, err)
	}
}
