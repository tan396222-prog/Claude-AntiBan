package workbench

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sylvinus/agent-vm/internal/cage"
)

func TestStopPreemptsJobWithoutLosingStopResult(t *testing.T) {
	s := testServer(t)
	started, cancelled, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	s.launch(httptest.NewRecorder(), "safety", func() error {
		ctx := s.operationContext()
		close(started)
		<-ctx.Done()
		close(cancelled)
		s.setMessage("stale result")
		_, _ = s.jobs.execution().Write([]byte("stale log"))
		return ctx.Err()
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("job did not start")
	}
	w := httptest.NewRecorder()
	s.launch(w, "stop", func() error { s.setMessage("stopped"); close(finished); return nil })
	if w.Code != 200 {
		t.Fatalf("stop rejected: %d", w.Code)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("old job was not cancelled")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("stop did not run")
	}
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.jobs.snapshot().Kind != "stop" || s.jobs.snapshot().Running || s.jobs.snapshot().Error != "" || s.jobs.snapshot().Log != "" {
		t.Fatalf("stale job overwrote stop: %+v", s.jobs.snapshot())
	}
}

func TestPauseBypassesBusyQueueAndRevokesCheck(t *testing.T) {
	s := testServer(t)
	cfg, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.profile, "state", "lima", cage.InstanceName(s.profile, s.environmentDir()))
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	g, err := cage.NewEgressGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	stop, err := cage.StartGateControl(s.configPath, dir, g)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if err = g.BeginCheck(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.setJobForTest(Job{Kind: "safety", Running: true})
	s.checkCancel = cancel
	w := request(s, "POST", "/api/safety/close", s.origin, "{}", true)
	if w.Code != 200 {
		t.Fatalf("pause rejected while checking: %d %s", w.Code, w.Body.String())
	}
	if ctx.Err() == nil || g.Status().Mode != cage.GateClosed || !s.networkPaused {
		t.Fatal("pause did not cancel and close the gate")
	}
}
