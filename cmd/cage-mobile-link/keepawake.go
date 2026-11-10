package main

import (
	"context"
	"log"

	"github.com/sylvinus/agent-vm/internal/keepawake"
)

func keepHostAwake(parent context.Context, logger *log.Logger) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		keepawake.Run(ctx, func(state keepawake.State) {
			if state.Error != "" {
				logger.Println("Host keep-awake unavailable:", state.Error)
			}
		})
	}()
	return func() { cancel(); <-done }
}
