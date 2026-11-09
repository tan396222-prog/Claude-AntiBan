package workbench

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/managedproxy"
)

// Keep the real request, job, source persistence and gate-control paths. Only
// external subscription traffic and the installed proxy process are replaced.
func pendingProxyApply(t *testing.T) (*Server, *cage.EgressGate, <-chan struct{}, func()) {
	t.Helper()
	s, gate := interfaceMonitorServer(t)
	p, err := managedproxy.Load(s.profile)
	if err != nil {
		t.Fatal(err)
	}
	p.Sources[0].URL = "https://subscription.example/nodes"
	p.Sources[0].Interval = 1
	p.Sources[0].Updated = time.Now().Add(-2 * time.Hour)
	p.Sources[0].Attempted = p.Sources[0].Updated
	if err := p.Save(s.profile); err != nil {
		t.Fatal(err)
	}
	validate, start, fetch := validateProxyCore, startProxyCore, fetchProxySource
	t.Cleanup(func() { validateProxyCore, startProxyCore, fetchProxySource = validate, start, fetch })
	validateProxyCore = func(_ context.Context, profile string, candidate managedproxy.Store) error {
		_, err := candidate.Runtime(profile)
		return err
	}
	fetchProxySource = func(_ context.Context, _ *http.Client, _ string) ([]byte, string, error) {
		return []byte(`proxies: [{name: qa, type: socks5, server: 127.0.0.1, port: 1, password: updated-test-value}]`), "", nil
	}
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	startProxyCore = func(ctx context.Context, _ string, _ managedproxy.Store) error {
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	t.Cleanup(func() { finish(); s.op.Lock(); s.op.Unlock() })
	return s, gate, started, finish
}

func awaitProxyStart(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("proxy application did not reach core startup")
	}
}

func pauseProxyNetwork(t *testing.T, s *Server) {
	t.Helper()
	if w := request(s, "POST", "/api/safety/close", s.origin, "{}", true); w.Code != 200 {
		t.Fatalf("pause rejected: %d %s", w.Code, w.Body.String())
	}
}

func assertProxyStayedPaused(t *testing.T, s *Server, gate *cage.EgressGate) {
	t.Helper()
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	paused, job := s.networkPaused, s.jobs.snapshot()
	s.mu.Unlock()
	if job.Running || job.Error != "" {
		t.Fatalf("proxy update did not complete: %+v", job)
	}
	if !paused || gate.Status().Mode != cage.GateClosed {
		t.Fatal("proxy update revoked manual pause")
	}
	p, err := managedproxy.Load(s.profile)
	if err != nil || p.Revision == "test-revision" {
		t.Fatal("proxy update did not persist a new revision", err)
	}
	cfg, err := loadConfig(s.configPath)
	if err != nil || gate.Status().Configuration != cfg.Fingerprint() {
		t.Fatal("paused gate was not synchronized to the applied proxy", err)
	}
}

func TestAutomaticSubscriptionUpdatePreservesManualPause(t *testing.T) {
	for _, pauseBefore := range []bool{true, false} {
		name := "paused before automatic refresh"
		if !pauseBefore {
			name = "paused while automatic refresh applies"
		}
		t.Run(name, func(t *testing.T) {
			s, gate, started, finish := pendingProxyApply(t)
			if pauseBefore {
				pauseProxyNetwork(t, s)
			}
			s.op.Lock()
			s.refreshProxySubscriptions()
			s.op.Unlock()
			awaitProxyStart(t, started)
			if !pauseBefore {
				pauseProxyNetwork(t, s)
			}
			finish()
			assertProxyStayedPaused(t, s, gate)
			p, _ := managedproxy.Load(s.profile)
			if p.Sources[0].Nodes[0].Config["password"] != "updated-test-value" {
				t.Fatal("automatic refresh did not consume the new subscription")
			}
		})
	}
}

func TestManualProxySelectionHonorsLaterPause(t *testing.T) {
	s, gate, started, finish := pendingProxyApply(t)
	pauseProxyNetwork(t, s)
	p, _ := managedproxy.Load(s.profile)
	if w := request(s, "POST", "/api/proxy/select", s.origin, `{"node_id":"`+p.Selected+`"}`, true); w.Code != 200 {
		t.Fatalf("selection rejected: %d %s", w.Code, w.Body.String())
	}
	awaitProxyStart(t, started)
	pauseProxyNetwork(t, s)
	finish()
	assertProxyStayedPaused(t, s, gate)
	reloaded := reloadedIntentServer(t, s)
	if err := reloaded.restoreNetworkIntent(); err != nil || !reloaded.networkPaused {
		t.Fatal("later manual pause was not durable", err)
	}
}

func TestManualProxySelectionMayResumeExistingPause(t *testing.T) {
	s, _, started, finish := pendingProxyApply(t)
	pauseProxyNetwork(t, s)
	// No VM is running in this case, so selecting a node may prepare networking
	// without trying to start a real guest as part of this unit test.
	path := filepath.Join(s.profile, "state", "lima", cage.InstanceName(s.profile, s.environmentDir()), "cage-running.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	p, _ := managedproxy.Load(s.profile)
	if w := request(s, "POST", "/api/proxy/select", s.origin, `{"node_id":"`+p.Selected+`"}`, true); w.Code != 200 {
		t.Fatalf("selection rejected: %d %s", w.Code, w.Body.String())
	}
	awaitProxyStart(t, started)
	finish()
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.jobs.snapshot().Running || s.jobs.snapshot().Error != "" || s.networkPaused {
		t.Fatalf("explicit selection failed to resume the existing pause: %+v", s.jobs.snapshot())
	}
	reloaded := reloadedIntentServer(t, s)
	if err := reloaded.restoreNetworkIntent(); err != nil || reloaded.networkPaused {
		t.Fatal("explicit node resume was not durable", err)
	}
}
