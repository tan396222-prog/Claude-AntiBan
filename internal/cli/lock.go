package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// lockVM takes the lock of name, which serialises what changes that VM
// between agent-vm runs: two runs in one folder would otherwise clean up,
// clone and configure the same VM at once. It waits, saying so, until the
// holder is done or ctx ends. The lock is a file in the state folder's
// locks/, kept for the next run: a crashed holder releases it with its
// process.
func (e *app) lockVM(ctx context.Context, name string) (unlock func(), err error) {
	dir := e.state.Path("locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	told := false
	for {
		ok, err := tryLock(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		if ok {
			// Closing the file releases the lock.
			return func() { f.Close() }, nil
		}
		if !told {
			fmt.Fprintf(e.io.Stderr, "Waiting for another agent-vm that is working on '%s'...\n", name)
			told = true
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
