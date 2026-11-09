package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestDesktopLocalProbeLeavesColdProfileUntouched(t *testing.T) {
	root := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	d := NewDesktopLocalController(filepath.Join(root, "local", "config.json"), exe)
	st, err := d.Status(context.Background())
	if err != nil || !st.Available || !st.Known || st.Running || st.ServiceRunning {
		t.Fatalf("cold probe: %+v %v", st, err)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("probe initialized a local workload: %v %v", entries, err)
	}
}

func TestDesktopLocalReleaseHasIndependentLauncherAndAccountGuards(t *testing.T) {
	s := testServer(t)
	path := filepath.Join(s.profile, "state", "lima", "fixture", "cage-running.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writePrivate(path, map[string]int{"pid": os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	s.account.lock()
	observed := s.desktopLocalSnapshot().ControllerID
	for _, tc := range []struct {
		name, token, identity string
		stop                  bool
		status                int
	}{
		{"browser cookie cannot stop", "", observed, true, 401},
		{"previous controller", s.token, "previous", true, 409},
		{"idle handoff cannot stop workload", s.token, observed, false, 409},
		{"locked account", s.token, observed, true, 423},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := json.Marshal(desktopLocalRequest{Token: tc.token, ControllerID: tc.identity, Running: true, Stop: tc.stop})
			got := request(s, "POST", "/api/desktop-local/release", s.origin, string(b), true)
			if got.Code != tc.status || s.jobs.snapshot().ID != "" {
				t.Fatalf("result=%d, job=%+v", got.Code, s.jobs.snapshot())
			}
		})
	}
	s.setJobForTest(Job{ID: "another-completed-job"})
	b, _ := json.Marshal(desktopLocalRequest{Token: s.token, ControllerID: observed, Running: true, Stop: true})
	if w := request(s, "POST", "/api/desktop-local/release", s.origin, string(b), true); w.Code != http.StatusConflict {
		t.Fatalf("old workload confirmation was accepted: %d", w.Code)
	}
}

func TestDesktopLocalStopMustFinishItsOwnJobBeforeHandoff(t *testing.T) {
	s := testServer(t)
	s.terminals.sessions = map[string]*terminalSession{"live": {id: "live"}}
	exited := make(chan struct{})
	s.shutdown = func() { close(exited) }
	release := func(input desktopLocalRequest) (int, desktopLocalReply) {
		input.Token = s.token
		b, _ := json.Marshal(input)
		w := request(s, "POST", "/api/desktop-local/release", s.origin, string(b), false)
		var reply desktopLocalReply
		if w.Code == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
				t.Fatal(err)
			}
		}
		return w.Code, reply
	}
	view := s.desktopLocalSnapshot()
	code, job := release(desktopLocalRequest{ControllerID: view.ControllerID, Terminals: 1, Stop: true})
	if code != 200 || !job.JobRunning || job.JobID == "" {
		t.Fatalf("stop %d %+v", code, job)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		view = s.desktopLocalSnapshot()
		if !view.JobRunning || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if view.JobID != job.JobID || view.JobFailed || view.JobRunning || view.LiveTerminals != 0 {
		t.Fatalf("job outcome %+v", view)
	}
	if code, _ := release(desktopLocalRequest{ControllerID: view.ControllerID, JobID: "different"}); code != 409 {
		t.Fatalf("handoff for different job %d", code)
	}
	if code, _ := release(desktopLocalRequest{ControllerID: view.ControllerID, JobID: job.JobID}); code != 200 {
		t.Fatalf("handoff %d", code)
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("approved idle owner did not exit")
	}
}

// A real full controller owns the lock and launch ticket. This test never
// boots a VM. It verifies that closing a view preserves its controller and
// that only its currently reported identity can release the idle process.
func TestDesktopLocalAuthenticatedViewAndIdleExit(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "local")
	config := filepath.Join(profile, "config.json")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestServeShutdownHandoff$", "-test.timeout=25s")
	cmd.Env = append(os.Environ(), "CAGE_TEST_SHUTDOWN_CONFIG="+config)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	})
	d := NewDesktopLocalController(config, exe)
	var st DesktopLocalStatus
	for {
		st, err = d.Status(ctx)
		if err == nil && st.Known && st.ServiceRunning {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("controller did not start")
		case <-done:
			t.Fatal("controller exited", waitErr)
		case <-time.After(50 * time.Millisecond):
		}
	}
	session, err := d.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	req, _ := http.NewRequestWithContext(ctx, "GET", session.Origin+"/api/account", nil)
	req.Header.Set("Cookie", "untrusted=123")
	req.Header.Set("Origin", "https://not-the-controller.invalid")
	res, err := session.Client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("authenticated view: %d", res.StatusCode)
	}
	if err = session.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := d.Status(ctx)
	if err != nil || !after.ServiceRunning || after.ControllerID != st.ControllerID {
		t.Fatalf("view closed controller: %+v %v", after, err)
	}
	if released, err := d.(*desktopLocal).ReleaseOwnedIdle(ctx, after); err != nil || released {
		t.Fatalf("independently started service auto-release: released=%v err=%v", released, err)
	}
	if after, err = d.Status(ctx); err != nil || !after.ServiceRunning || after.ControllerID != st.ControllerID {
		t.Fatalf("independent owner was lost: %+v %v", after, err)
	}
	stale := st
	stale.ControllerID = fmt.Sprintf("%064x", 1)
	if err = d.Release(ctx, stale, false); err == nil {
		t.Fatal("stale handoff accepted")
	}
	if err = d.Release(ctx, st, false); err != nil {
		t.Fatal(err)
	}
	<-done
	if waitErr != nil {
		t.Fatal(waitErr)
	}
	if _, err := os.Stat(filepath.Join(profile, "control.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("released controller record still exists: %v", err)
	}
}
