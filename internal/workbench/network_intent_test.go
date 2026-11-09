package workbench

import (
	"github.com/sylvinus/agent-vm/internal/host"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sylvinus/agent-vm/internal/cage"
)

func reloadedIntentServer(t *testing.T, previous *Server) *Server {
	t.Helper()
	reloaded := &Server{profile: previous.profile, configPath: previous.configPath, origin: previous.origin, token: previous.token, session: previous.session, preferences: previous.preferences}
	return reloaded
}

func TestReloadedManualPauseSurvivesAutomaticSubscriptionRefresh(t *testing.T) {
	for _, record := range []string{"saved", "corrupt", "missing"} {
		t.Run(record, func(t *testing.T) {
			original, gate, started, finish := pendingProxyApply(t)
			pauseProxyNetwork(t, original)
			intentPath := filepath.Join(original.profile, "network-intent.json")
			if record == "corrupt" {
				if err := os.WriteFile(intentPath, []byte(`{"paused":false}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if record == "missing" {
				if err := os.Remove(intentPath); err != nil {
					t.Fatal(err)
				}
			}
			reloaded := reloadedIntentServer(t, original)
			err := reloaded.restoreNetworkIntent()
			if record == "corrupt" {
				if err == nil {
					t.Fatal("corrupt intent was accepted")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !reloaded.networkPaused {
				t.Fatal("service reload discarded manual pause")
			}
			if record == "saved" {
				if err := host.CheckPrivateFile(intentPath); err != nil {
					t.Fatal("intent is not private", err)
				}
				if reloaded.safety.pauseVersion != original.safety.pauseVersion {
					t.Fatal("pause generation was lost")
				}
			}
			reloaded.op.Lock()
			reloaded.refreshProxySubscriptions()
			reloaded.op.Unlock()
			awaitProxyStart(t, started)
			finish()
			assertProxyStayedPaused(t, reloaded, gate)
			again := reloadedIntentServer(t, reloaded)
			_ = again.restoreNetworkIntent()
			if !again.networkPaused {
				t.Fatal("automatic refresh persisted a resume")
			}
		})
	}
}

func TestExplicitResumePersistsAcrossServiceReload(t *testing.T) {
	for _, kind := range []string{"start", "safety", "restore"} {
		t.Run(kind, func(t *testing.T) {
			s, gate := interfaceMonitorServer(t)
			pauseProxyNetwork(t, s)
			finished := make(chan struct{})
			response := httptest.NewRecorder()
			s.launch(response, kind, func() error { close(finished); return nil })
			if response.Code != 200 {
				t.Fatal(response.Code, response.Body.String())
			}
			select {
			case <-finished:
			case <-time.After(3 * time.Second):
				t.Fatal("explicit resume job did not finish")
			}
			s.op.Lock()
			s.op.Unlock()
			reloaded := reloadedIntentServer(t, s)
			if err := reloaded.restoreNetworkIntent(); err != nil {
				t.Fatal(err)
			}
			if reloaded.networkPaused {
				t.Fatal("explicit resume was not persisted")
			}
			if gate.Status().Mode != cage.GateClosed {
				t.Fatal("recording intent itself granted networking")
			}
		})
	}
}

func TestPauseAndResumeStorageFailuresKeepGateClosed(t *testing.T) {
	for _, operation := range []string{"pause", "resume"} {
		t.Run(operation, func(t *testing.T) {
			s, gate := grantedToolUpdateServer(t)
			s.setJobForTest(Job{})
			if err := os.Mkdir(filepath.Join(s.profile, "network-intent.json"), 0700); err != nil {
				t.Fatal(err)
			}
			var response *httptest.ResponseRecorder
			if operation == "pause" {
				response = request(s, "POST", "/api/safety/close", s.origin, "{}", true)
			} else {
				response = httptest.NewRecorder()
				s.launch(response, "start", func() error { t.Error("resume ran without durable intent"); return nil })
			}
			if response.Code != 400 || !strings.Contains(response.Body.String(), "无法保存") {
				t.Fatalf("storage failure not reported: %d %s", response.Code, response.Body.String())
			}
			if !s.networkPaused || gate.Status().Mode != cage.GateClosed {
				t.Fatal("storage failure left external networking enabled")
			}
		})
	}
}

func TestQueuedSettingsApplyCannotClearLaterPersistentPause(t *testing.T) {
	s, gate := interfaceMonitorServer(t)
	pauseProxyNetwork(t, s)
	// Keep the real settings write, but there is no live VM to stop in this fixture.
	if err := os.Remove(filepath.Join(s.profile, "state", "lima", cage.InstanceName(s.profile, s.environmentDir()), "cage-running.json")); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Timezone = "Asia/Tokyo"
	next := savedProfile{Config: cfg, Preferences: s.preferences}
	finished := make(chan error, 1)
	s.op.Lock()
	response := httptest.NewRecorder()
	s.launch(response, "settings", func() error { err := s.apply(next, false); finished <- err; return err })
	if response.Code != 200 {
		s.op.Unlock()
		t.Fatal(response.Code)
	}
	pauseProxyNetwork(t, s)
	s.op.Unlock()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("settings apply did not finish")
	}
	s.op.Lock()
	s.op.Unlock()
	if !s.networkPaused || gate.Status().Mode != cage.GateClosed {
		t.Fatal("older settings action cleared a later pause")
	}
	reloaded := reloadedIntentServer(t, s)
	if err := reloaded.restoreNetworkIntent(); err != nil {
		t.Fatal(err)
	}
	if !reloaded.networkPaused {
		t.Fatal("older settings action persisted a resume")
	}
	actual, err := loadConfig(s.configPath)
	if err != nil || actual.Timezone != "Asia/Tokyo" {
		t.Fatal("settings did not complete", err)
	}
}
