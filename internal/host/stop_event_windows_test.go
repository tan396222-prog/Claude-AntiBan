package host

import (
	"path/filepath"
	"testing"
	"time"
)

func TestInstanceStopEventWorksWithoutConsoleAndStaysScoped(t *testing.T) {
	dir := t.TempDir()
	notified := make(chan struct{}, 1)
	stop, err := ListenInstanceStop(dir, func() { notified <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	if requested, err := RequestInstanceStop(filepath.Join(dir, "other")); err != nil || requested {
		t.Fatalf("another instance received stop: %v %v", requested, err)
	}
	if requested, err := RequestInstanceStop(dir); err != nil || !requested {
		t.Fatalf("stop was not delivered: %v %v", requested, err)
	}
	select {
	case <-notified:
	case <-time.After(2 * time.Second):
		t.Fatal("stop event was not received")
	}
	stop()
	if requested, err := RequestInstanceStop(dir); err != nil || requested {
		t.Fatalf("closed event retained a listener: %v %v", requested, err)
	}
}
