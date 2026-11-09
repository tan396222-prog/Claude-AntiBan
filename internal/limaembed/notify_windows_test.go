package limaembed

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lima-vm/lima/v2/pkg/hostagent/events"
)

type countedSyncFile struct {
	*os.File
	syncs atomic.Int32
}

func (w *countedSyncFile) Sync() error {
	w.syncs.Add(1)
	return w.File.Sync()
}

// This uses Windows file notifications and the real Lima event watcher. It
// starts no VM and writes only synthetic events in the test's temp directory.
func TestWindowsEventWatchFlush(t *testing.T) {
	dir := t.TempDir()
	stdoutPath, stderrPath := filepath.Join(dir, "ha.stdout.log"), filepath.Join(dir, "ha.stderr.log")
	file, err := os.Create(stdoutPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := os.WriteFile(stderrPath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	initial, running, done := make(chan struct{}, 1), make(chan struct{}, 1), make(chan error, 1)
	go func() {
		done <- events.Watch(ctx, stdoutPath, stderrPath, time.Time{}, false, func(ev events.Event) bool {
			if ev.Status.Running {
				running <- struct{}{}
				return true
			}
			select {
			case initial <- struct{}{}:
			default:
			}
			return false
		})
	}()
	_, err = fmt.Fprintf(file, "{\"time\":%q,\"status\":{}}\n", time.Now().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-initial:
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not observe initial event")
	}
	// Let the real tailer return to waiting at EOF before the measured write.
	time.Sleep(200 * time.Millisecond)
	counted := &countedSyncFile{File: file}
	writer := &syncWriter{w: newEventWatch(counted, "synthetic-test")}
	start := time.Now()
	_, err = fmt.Fprintf(writer, "{\"time\":%q,\"status\":{\"running\":true}}\n", time.Now().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	received := false
	select {
	case <-running:
		received = true
	case <-time.After(2 * time.Second):
	}
	t.Logf("runningReceived=%t latency=%s forwardedSyncCalls=%d", received, time.Since(start), counted.syncs.Load())
	writer.flush()
	cancel()
	file.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watcher cleanup timed out")
	}
	if !received {
		t.Fatal("hostagent running event did not reach the file watcher")
	}
	if counted.syncs.Load() == 0 {
		t.Fatal("notification wrapper did not forward Sync to the log file")
	}
}
