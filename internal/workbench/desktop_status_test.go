package workbench

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestDesktopStatusSharesReadsAndRefreshesAfterAction(t *testing.T) {
	var cache desktopStatusCache
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	load := func(ctx context.Context) ([]byte, error) {
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return []byte(`{"running":true}`), nil
	}
	first := make(chan error, 1)
	ctx, cancel := context.WithCancel(t.Context())
	go func() { _, err := cache.read(ctx, t.Context(), load); first <- err }()
	<-started
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter: %v", err)
	}
	close(release)
	if _, err := cache.read(t.Context(), t.Context(), load); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("shared status read executed %d times", calls.Load())
	}
	cache.invalidate()
	if _, err := cache.read(t.Context(), t.Context(), load); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("action did not refresh state: %d", calls.Load())
	}
}

func TestDesktopStatusExpiresWithoutAnAction(t *testing.T) {
	var cache desktopStatusCache
	load := func(context.Context) ([]byte, error) { return []byte(`{"running":true}`), nil }
	if _, err := cache.read(t.Context(), t.Context(), load); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2100 * time.Millisecond)
	got, err := cache.read(t.Context(), t.Context(), func(context.Context) ([]byte, error) { return []byte(`{"running":false}`), nil })
	if err != nil || string(got) != `{"running":false}` {
		t.Fatalf("external app stop remained stale: %s %v", got, err)
	}
}

func TestDesktopStatusDoesNotCacheFailedProbe(t *testing.T) {
	var cache desktopStatusCache
	var calls int
	load := func(context.Context) ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("guest disconnected")
		}
		return []byte(`{"running":true}`), nil
	}
	if _, err := cache.read(t.Context(), t.Context(), load); err == nil {
		t.Fatal("lost guest was reported as healthy")
	}
	if _, err := cache.read(t.Context(), t.Context(), load); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("failure prevented recovery: %d", calls)
	}
}

func TestDesktopStatusOldProbeCannotOverwriteAction(t *testing.T) {
	var cache desktopStatusCache
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = cache.read(t.Context(), t.Context(), func(context.Context) ([]byte, error) {
			close(started)
			<-release
			return []byte(`{"running":false}`), nil
		})
	}()
	<-started
	cache.invalidate()
	load := func(context.Context) ([]byte, error) { return []byte(`{"running":true}`), nil }
	if _, err := cache.read(t.Context(), t.Context(), load); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-done
	got, err := cache.read(t.Context(), t.Context(), func(context.Context) ([]byte, error) { t.Error("new state lost from cache"); return nil, nil })
	if err != nil || string(got) != `{"running":true}` {
		t.Fatalf("stale probe replaced new state: %s %v", got, err)
	}
}
