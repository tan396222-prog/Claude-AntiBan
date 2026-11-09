package workbench

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAlreadyAdmittedHandoffsShutDownOnlyOnce(t *testing.T) {
	s := testServer(t)
	var count atomic.Int32
	stopped := make(chan struct{}, 16)
	s.shutdown = func() { count.Add(1); stopped <- struct{}{} }
	var callers sync.WaitGroup
	// These requests have already passed the handler's activity admission. A
	// later request must recheck closing once it acquires the operation lock.
	for range 16 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			r := httptest.NewRequest("POST", s.origin+"/api/handoff", strings.NewReader(`{"token":"`+s.token+`"}`))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			s.handoff(w, r)
			if w.Code != 200 && w.Code != 409 && w.Code != 503 {
				t.Errorf("unexpected handoff response: %d", w.Code)
			}
		}()
	}
	callers.Wait()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("handoff did not shut down")
	}
	// A deterministic late admitted request also covers the scheduling case
	// where all simultaneous callers above happened to observe a locked op.
	r := httptest.NewRequest("POST", s.origin+"/api/handoff", strings.NewReader(`{"token":"`+s.token+`"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handoff(w, r)
	if w.Code != http.StatusServiceUnavailable || count.Load() != 1 {
		t.Fatalf("repeated handoff restarted shutdown: status=%d calls=%d", w.Code, count.Load())
	}
}

func TestHandoffRequiresLauncherSecretAndIdleEnvironment(t *testing.T) {
	for _, busy := range []string{"none", "job", "terminal", "vm", "operation"} {
		t.Run(busy, func(t *testing.T) {
			s := testServer(t)
			var stopped atomic.Bool
			s.shutdown = func() { stopped.Store(true) }
			switch busy {
			case "job":
				s.setJobForTest(Job{ID: "active", Running: true})
			case "terminal":
				s.terminals.sessions = map[string]*terminalSession{"live": {}}
			case "vm":
				path := filepath.Join(s.profile, "state", "lima", "base-building", "cage-running.json")
				if err := writePrivate(path, map[string]int{"pid": os.Getpid()}); err != nil {
					t.Fatal(err)
				}
			case "operation":
				s.op.Lock()
				defer s.op.Unlock()
			}
			if w := request(s, "POST", "/api/handoff", s.origin, `{"token":"wrong"}`, true); w.Code != 401 {
				t.Fatalf("session bypassed launcher secret: %d", w.Code)
			}
			if w := request(s, "POST", "/api/handoff", "https://elsewhere.example", `{"token":"`+s.token+`"}`, false); w.Code != 403 {
				t.Fatalf("cross-origin handoff: %d", w.Code)
			}
			w := request(s, "POST", "/api/handoff", s.origin, `{"token":"`+s.token+`"}`, false)
			if busy != "none" {
				if w.Code != 409 || stopped.Load() {
					t.Fatalf("terminated busy controller: %d", w.Code)
				}
				return
			}
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			deadline := time.Now().Add(time.Second)
			for !stopped.Load() && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if !stopped.Load() {
				t.Fatal("idle controller did not shut down")
			}
			launchResult := httptest.NewRecorder()
			s.launch(launchResult, "start", func() error { t.Error("new work started after handoff"); return nil })
			if launchResult.Code != http.StatusServiceUnavailable {
				t.Fatalf("new job accepted after handoff: %d", launchResult.Code)
			}
			if w := request(s, "POST", "/api/terminals", s.origin, `{}`, true); w.Code != 503 {
				t.Fatalf("terminal accepted after handoff: %d", w.Code)
			}
		})
	}
}

func TestDesktopRejectsUnknownAndDifferentBuildsWithoutStoppingThem(t *testing.T) {
	current, err := currentController()
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"same", "same-version-other-build", "legacy"} {
		t.Run(kind, func(t *testing.T) {
			profile := t.TempDir()
			identity := current
			if kind == "same-version-other-build" {
				identity.Build = "different-build"
			}
			if kind == "legacy" {
				identity = ControllerIdentity{}
			}
			var stopCalled bool
			var origin string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/launch":
					jsonReply(w, launchResponse{URL: origin + "/#ticket", Controller: identity})
				case "/api/handoff":
					http.Error(w, "busy", 409)
				case "/api/stop":
					stopCalled = true
					http.Error(w, "unexpected", 500)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			_, portString, _ := net.SplitHostPort(server.Listener.Addr().String())
			port, _ := strconv.Atoi(portString)
			origin = "http://localhost:" + portString
			if err := writePrivate(filepath.Join(profile, "control.json"), controlFile{Port: port, Token: string(make([]byte, 64)), PID: os.Getpid()}); err != nil {
				t.Fatal(err)
			}
			entry, err := Desktop(filepath.Join(profile, "config.json"), DesktopOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if kind == "same" {
				if entry.Status != "ready" || entry.URL == "" {
					t.Fatalf("same build refused: %+v", entry)
				}
			} else if entry.Status != "upgrade_pending" || entry.URL != "" {
				t.Fatalf("old build silently connected: %+v", entry)
			}
			if stopCalled {
				t.Fatal("startup stopped existing work")
			}
			if kind == "legacy" {
				if _, err := Desktop(filepath.Join(profile, "config.json"), DesktopOptions{CompleteUpdate: true}); err == nil {
					t.Fatal("unverified legacy PID accepted")
				}
				if stopCalled {
					t.Fatal("signalled or stopped unverified legacy process")
				}
			}
		})
	}
}

func TestPrepareUpdateDoesNotAcceptUnresponsiveLockedController(t *testing.T) {
	profile := t.TempDir()
	f, err := os.OpenFile(filepath.Join(profile, "control.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = lockController(f); err != nil {
		t.Fatal(err)
	}
	defer unlockController(f)
	if _, err := PrepareUpdate(filepath.Join(profile, "config.json"), false); err == nil {
		t.Fatal("locked offline service accepted as safe to replace")
	}
}
