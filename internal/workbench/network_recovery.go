package workbench

import (
	"context"
	"errors"
	"time"

	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/diagnostics"
)

type networkRecovery struct {
	RunID, Configuration string
	PauseVersion         uint64
	Epoch                uint64
	Next                 time.Time
	Attempts             int
}

func (s *Server) scheduleNetworkRecovery(state cage.GateStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.networkPaused || s.closing || state.Mode != cage.GateClosed || state.Report == nil {
		return
	}
	s.recovery = networkRecovery{RunID: state.RunID, Configuration: state.Configuration, Epoch: state.Epoch, PauseVersion: s.safety.pauseVersion, Next: time.Now().Add(15 * time.Second)}
	diagnostics.Log("warn", "network", "temporary monitoring failure; full recheck scheduled: "+state.Reason)
}

func recoveryMatches(r networkRecovery, state cage.GateStatus, configuration string, paused bool, generation uint64) bool {
	return r.RunID != "" && !paused && generation == r.PauseVersion && state.RunID == r.RunID && state.Configuration == r.Configuration && configuration == r.Configuration && state.Epoch == r.Epoch && state.Mode == cage.GateClosed
}

// Called with s.op held. Admission uses the ordinary job queue and never changes
// saved network intent. The worker rechecks after acquiring s.op as well.
func (s *Server) recoverNetwork() {
	s.mu.Lock()
	r := s.recovery
	blocked := s.closing || s.networkPaused || !s.ready || s.jobs.snapshot().Running || time.Now().Before(r.Next)
	s.mu.Unlock()
	if blocked || r.RunID == "" {
		return
	}
	// The resource manager refreshes UpdatedAt even when a sample fails.
	// Only a recent pressure measurement should postpone the full gate check.
	resources, err := cage.ReadResourceStatus(s.instanceDir())
	guestPressure := resources.Pressure && time.Since(resources.ObservedAt) <= 10*time.Second
	if err == nil && (guestPressure || resources.HostPressure) {
		return
	}
	_, _ = s.submitJob("network-recovery", func() error { return s.runNetworkRecovery(r, s.runSafetyCheck) })
}

func (s *Server) runNetworkRecovery(expected networkRecovery, recheck func() error) error {
	s.mu.Lock()
	stopped := s.closing || s.networkPaused || s.safety.pauseVersion != expected.PauseVersion
	if stopped {
		s.recovery = networkRecovery{}
	}
	s.mu.Unlock()
	if stopped {
		return context.Canceled
	}
	client, err := s.gateClient()
	if err != nil {
		return s.finishNetworkRecovery(expected, err)
	}
	ctx, cancel := context.WithTimeout(s.operationContext(), 5*time.Second)
	state, err := client.Status(ctx)
	cancel()
	if err != nil {
		return s.finishNetworkRecovery(expected, err)
	}
	cfg, err := loadConfig(s.configPath)
	if err != nil {
		return s.finishNetworkRecovery(expected, err)
	}
	s.mu.Lock()
	valid := !s.closing && s.recovery == expected && recoveryMatches(expected, state, cfg.Fingerprint(), s.networkPaused, s.safety.pauseVersion)
	if !valid {
		s.recovery = networkRecovery{}
	}
	s.mu.Unlock()
	if !valid {
		return context.Canceled
	}
	if err := s.operationContext().Err(); err != nil {
		return err
	}
	s.setMessage("正在重新检查环境与出口，通过后自动恢复联网…")
	err = recheck()
	return s.finishNetworkRecovery(expected, err)
}

func (s *Server) finishNetworkRecovery(expected networkRecovery, err error) error {
	// A failed full check changes the gate epoch. Keep that exact epoch for the
	// next attempt, while preserving user/configuration changes as cancellation.
	var current cage.GateStatus
	if err != nil {
		if client, e := s.gateClient(); e == nil {
			ctx, cancel := context.WithTimeout(s.lifetimeContext(), 3*time.Second)
			current, _ = client.Status(ctx)
			cancel()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recovery != expected {
		return err
	}
	if err == nil || s.closing || s.networkPaused || s.safety.pauseVersion != expected.PauseVersion || errors.Is(err, context.Canceled) {
		s.recovery = networkRecovery{}
		return err
	}
	if current.RunID != "" {
		if current.RunID != expected.RunID || current.Configuration != expected.Configuration || current.Mode != cage.GateClosed {
			s.recovery = networkRecovery{}
			return err
		}
		s.recovery.Epoch = current.Epoch
	}
	s.recovery.Attempts++
	s.recovery.Next = time.Now().Add(time.Duration(min(300, 15<<min(s.recovery.Attempts, 5))) * time.Second)
	return err
}
