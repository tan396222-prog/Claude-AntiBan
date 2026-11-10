package workbench

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// Each controller shares one status probe and briefly reuses its observations.
// Cached observations never authorize input or capture. Actions discard them.
type desktopStatusCache struct {
	mu         sync.Mutex
	generation uint64
	data       []byte
	expires    time.Time
	pending    *desktopStatusProbe
}

type desktopStatusProbe struct {
	done chan struct{}
	data []byte
	err  error
}

func (c *desktopStatusCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	c.data, c.pending = nil, nil
}

func (c *desktopStatusCache) read(ctx, lifetime context.Context, load func(context.Context) ([]byte, error)) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := lifetime.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.data != nil && time.Now().Before(c.expires) {
		data := c.data
		c.mu.Unlock()
		return data, nil
	}
	pending := c.pending
	if pending == nil {
		pending = &desktopStatusProbe{done: make(chan struct{})}
		c.pending = pending
		generation := c.generation
		go func() {
			probeCtx, cancel := context.WithTimeout(lifetime, 12*time.Second)
			defer cancel()
			data, err := load(probeCtx)
			var result struct {
				Error string `json:"error"`
			}
			cacheable := err == nil && probeCtx.Err() == nil && json.Unmarshal(data, &result) == nil && result.Error == ""
			c.mu.Lock()
			defer c.mu.Unlock()
			pending.data, pending.err = data, err
			if generation == c.generation {
				if cacheable {
					c.data, c.expires = data, time.Now().Add(2*time.Second)
				}
				if c.pending == pending {
					c.pending = nil
				}
			}
			close(pending.done)
		}()
	}
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-pending.done:
		return pending.data, pending.err
	}
}
