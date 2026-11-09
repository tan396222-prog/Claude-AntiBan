package cage

import (
	"testing"
	"time"
)

func TestJobStopCoversLateAndRunningHostagents(t *testing.T) {
	profile := t.TempDir()
	id := "0123456789ab"
	called := make(chan struct{}, 1)
	stop, err := WatchJobStop(profile, id, func() { called <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if err = RequestJobStop(profile, id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("running hostagent did not observe cancellation")
	}
	if _, err = WatchJobStop(profile, id, func() { t.Error("late startup callback") }); err == nil {
		t.Fatal("cancelled job could start a detached hostagent")
	}
	fresh, err := WatchJobStop(profile, "abcdef012345", func() { t.Error("new job was cancelled") })
	if err != nil {
		t.Fatal(err)
	}
	fresh()
	if err = RequestJobStop(profile, "../../unrelated"); err == nil {
		t.Fatal("accepted a path as job ID")
	}
}
