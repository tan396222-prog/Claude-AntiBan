package workbench

import (
	"fmt"
	"strings"
	"time"

	"github.com/sylvinus/agent-vm/internal/diagnostics"
)

// Keep stage measurements in memory until the round ends so log writes do not
// consume the time available to the next probe or the lease renewal.
type monitorTiming struct {
	started, checkpoint time.Time
	until, verified     time.Time
	result              string
	stages              []string
}

func (s *Server) startMonitorTiming() *monitorTiming {
	now := time.Now()
	timing := &monitorTiming{started: now, checkpoint: now, result: "completed"}
	s.mu.Lock()
	timing.verified = s.safety.VerifiedAt
	if s.safety.Gate != nil {
		timing.until = s.safety.Gate.Until
	}
	s.mu.Unlock()
	return timing
}

func (t *monitorTiming) stage(name string) {
	now := time.Now()
	t.stages = append(t.stages, fmt.Sprintf("%s_ms=%d", name, now.Sub(t.checkpoint).Milliseconds()))
	t.checkpoint = now
}

func (s *Server) recordMonitorTiming(kind string, timing *monitorTiming) {
	if s.profile == "" {
		return
	}
	finished := time.Now()
	age, remainingStart, remainingEnd := int64(-1), int64(-1), int64(-1)
	if !timing.verified.IsZero() {
		age = timing.started.Sub(timing.verified).Milliseconds()
	}
	if !timing.until.IsZero() {
		remainingStart = timing.until.Sub(timing.started).Milliseconds()
		remainingEnd = timing.until.Sub(finished).Milliseconds()
	}
	_ = diagnostics.New(s.profile).Record("info", "network-monitor", fmt.Sprintf(
		"monitor=%s started=%s duration_ms=%d result=%s verified_age_ms=%d lease_known=%t lease_remaining_start_ms=%d lease_remaining_end_ms=%d %s",
		kind, timing.started.UTC().Format(time.RFC3339Nano), finished.Sub(timing.started).Milliseconds(),
		timing.result, age, !timing.until.IsZero(), remainingStart, remainingEnd, strings.Join(timing.stages, " ")))
}
