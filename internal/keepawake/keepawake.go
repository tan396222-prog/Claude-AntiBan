// Package keepawake holds a host power request without keeping its display on.
package keepawake

import (
	"context"
	"time"
)

type State struct {
	Active bool   `json:"active"`
	Error  string `json:"error,omitempty"`
}

// Run owns the request until ctx ends, including time between phone connections.
// A failed or unexpectedly exited helper is reported and retried. The caller
// must wait for Run to return when shutting down its hosting service.
func Run(ctx context.Context, report func(State)) {
	run(ctx, report, hold)
}

func run(ctx context.Context, report func(State), acquire func(context.Context, func()) error) {
	var previous State
	emit := func(state State) {
		if state != previous {
			previous = state
			report(state)
		}
	}
	delay := time.Second
	defer emit(State{})
	for ctx.Err() == nil {
		err := acquire(ctx, func() { emit(State{Active: true}); delay = time.Second })
		if ctx.Err() != nil {
			return
		}
		message := "主机防休眠请求已结束"
		if err != nil {
			message = err.Error()
		}
		emit(State{Error: message})
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay = min(30*time.Second, delay*2)
	}
}
