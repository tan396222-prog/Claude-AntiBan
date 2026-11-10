package keepawake

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetryAndRelease(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	states := make(chan State, 8)
	done := make(chan struct{})
	released := make(chan struct{})
	attempts := 0
	go func() {
		defer close(done)
		run(ctx, func(state State) { states <- state }, func(ctx context.Context, ready func()) error {
			attempts++
			if attempts == 1 {
				return errors.New("inhibitor unavailable")
			}
			ready()
			<-ctx.Done()
			close(released)
			return ctx.Err()
		})
	}()
	read := func() State {
		t.Helper()
		select {
		case state := <-states:
			return state
		case <-time.After(3 * time.Second):
			t.Fatal("missing power request state")
			return State{}
		}
	}
	if state := read(); state.Active || state.Error == "" {
		t.Fatal("failed request was hidden", state)
	}
	if state := read(); !state.Active || state.Error != "" {
		t.Fatal("request did not recover", state)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("request leaked after cancellation")
	}
	select {
	case <-released:
	default:
		t.Fatal("native request was not released")
	}
	if state := read(); state != (State{}) {
		t.Fatal("stopped request remained active", state)
	}
}
