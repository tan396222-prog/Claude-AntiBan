package workbench

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestCloseBackgroundCancelsControlAndSafetyChecks(t *testing.T) {
	s := &Server{}
	control, cancelControl := s.controlContext()
	defer cancelControl()
	check, cancelCheck := context.WithCancel(context.Background())
	defer cancelCheck()
	monitor, cancelMonitor := context.WithCancel(context.Background())
	defer cancelMonitor()
	s.checkCancel, s.monitorCancel = cancelCheck, cancelMonitor
	if deadline, ok := control.Deadline(); !ok || time.Until(deadline) > 5*time.Second {
		t.Fatal("guest control has no bounded deadline")
	}
	s.closeBackground()
	for name, ctx := range map[string]context.Context{"control": control, "check": check, "monitor": monitor} {
		if ctx.Err() != context.Canceled {
			t.Fatalf("%s survived controller shutdown: %v", name, ctx.Err())
		}
	}
	late, cancelLate := s.controlContext()
	defer cancelLate()
	if late.Err() != context.Canceled {
		t.Fatal("new control request survived controller shutdown")
	}
}

func TestClosingControllerCannotStartMonitors(t *testing.T) {
	// A service can enter closing before anything initializes its lifetime.
	// Concurrent late callers must observe cancellation and cannot add work
	// after closeBackground has begun waiting for the monitors.
	s := &Server{closing: true}
	var callers sync.WaitGroup
	for range 8 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			s.startMonitors()
			if s.lifetimeContext().Err() != context.Canceled {
				t.Error("closing service created a live context")
			}
		}()
	}
	s.closeBackground()
	callers.Wait()
	s.closeBackground()
}

func TestCloseBackgroundDrainsStartedMonitorLoops(t *testing.T) {
	s := &Server{}
	// Keep monitor iterations away from external I/O while exercising the real
	// loops. Cancellation must wake both loops without waiting for the next tick.
	s.op.Lock()
	defer s.op.Unlock()
	s.startMonitors()
	done := make(chan struct{})
	go func() { s.closeBackground(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("started monitor loops survived shutdown")
	}
}

func TestCloseBackgroundWaitsForJobAndRejectsNewWork(t *testing.T) {
	s := &Server{}
	started := make(chan struct{})
	cancelled := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	var releaseOnce sync.Once
	finishJob := func() { releaseOnce.Do(func() { close(release) }) }
	defer finishJob()
	_, err := s.jobs.submit(&s.op, "start", 0, nil, nil, func() error {
		ctx := s.jobs.execution().ctx
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-release
		return ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("job did not start")
	}
	go func() {
		s.closeBackground()
		close(done)
	}()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel the job")
	}
	select {
	case <-done:
		t.Fatal("shutdown returned before the job released its resources")
	default:
	}
	if _, err := s.jobs.submit(&s.op, "stop", 0, nil, nil, func() error {
		t.Error("shutdown admitted a new job")
		return nil
	}); !errors.Is(err, errJobsClosed) {
		t.Fatalf("new submission after shutdown: %v", err)
	}
	finishJob()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish after the job returned")
	}
	if job := s.jobs.snapshot(); job.Running || job.Message != "已取消" {
		t.Fatalf("shutdown left a running job: %+v", job)
	}
}
