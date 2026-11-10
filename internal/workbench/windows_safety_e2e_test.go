package workbench

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/guestos"
	"github.com/sylvinus/agent-vm/internal/host"
)

// Uses a supplied private QA profile and its actual selected proxy. Every
// critical check and the grant are produced by the shipped HTTP job handler.
func TestWindowsGuestSafetyWorkbench(t *testing.T) {
	profile, executable := os.Getenv("CAGE_WINDOWS_GUEST_QA_PROFILE"), os.Getenv("CAGE_WINDOWS_GUEST_QA_EXECUTABLE")
	if runtime.GOOS != "windows" || profile == "" || executable == "" {
		t.Skip("requires an isolated paired Windows profile and current Cage executable")
	}
	binding, err := guestos.LoadWindowsProfile(profile)
	if err != nil || binding == nil || binding.NetworkPairingFile == "" {
		t.Fatal("paired packet-network profile unavailable")
	}
	s := testServer(t)
	s.profile, s.configPath, s.exe, s.windowsGuest = profile, filepath.Join(profile, "config.json"), executable, binding
	s.preferences = Preferences{Workspace: s.environmentDir(), Keyboard: "windows"}
	s.lifetimeContext()
	defer s.lifeCancel()
	defer s.closeWindowsBrowser()
	defer s.closeWindowsNetwork()
	defer s.closeProjectShares()
	if err = host.EnsurePrivateDir(s.environmentDir()); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = cfg.RequireManagedProxy(profile); err != nil {
		t.Fatal("QA managed proxy configuration is not internally consistent")
	}
	ctx, cancel := context.WithTimeout(s.lifetimeContext(), 15*time.Second)
	err = s.applyWindowsGuestSettings(ctx, cfg)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(s.handler())
	defer server.Close()
	s.origin = server.URL
	request, _ := http.NewRequest("POST", server.URL+"/api/safety/check", strings.NewReader("{}"))
	request.Header.Set("Origin", server.URL)
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: s.cookieName(), Value: s.session})
	request.AddCookie(&http.Cookie{Name: s.cookieName() + "-desktop", Value: trustedViewProof(s.token)})
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var job Job
	err = json.NewDecoder(response.Body).Decode(&job)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || job.ID == "" {
		t.Fatal("real safety job was not admitted")
	}
	deadline := time.Now().Add(190 * time.Second)
	for s.jobs.snapshot().Running && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	job = s.jobs.snapshot()
	if job.Running {
		s.jobs.cancelRunning()
		t.Fatal("real safety job timed out")
	}
	s.mu.Lock()
	report, gate := s.safety.Report, s.safety.Gate
	s.mu.Unlock()
	if report == nil {
		t.Fatalf("real checks produced no report (job error: %s)", job.Error)
	}
	for _, check := range report.Checks {
		t.Logf("actual check %s: %s", check.ID, check.State)
	}
	if job.Error != "" || gate == nil || gate.Mode != cage.GateOpen || gate.Report == nil {
		t.Fatalf("real checks did not authorize the gate: %s", job.Error)
	}
	if err = report.Authorizes(gate.RunID, cfg.Fingerprint(), time.Now()); err != nil {
		t.Fatal(err)
	}
	t.Log("Actual Windows workbench safety report authorized the selected proxy; no fabricated check or grant was used")
	// An explicit QA-only window permits normal evaluation activation while the
	// real controller continues lease supervision. It is never an open bypass.
	if os.Getenv("CAGE_WINDOWS_GUEST_QA_ACTIVATION_WINDOW") == "1" {
		t.Log("Actual gate open; 45-second supervised QA activation window started")
		until := time.Now().Add(45 * time.Second)
		for time.Now().Before(until) {
			s.monitorSafety()
			time.Sleep(time.Second)
		}
	}
}
