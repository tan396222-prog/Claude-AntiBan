package workbench

import (
	"context"
	"fmt"
)

func (s *Server) busy() bool { return s.jobs.snapshot().Running }

// submitJob keeps network intent and account policy in the workbench. The
// manager owns admission, execution, cancellation and published job state.
func (s *Server) submitJob(kind string, run func() error) (Job, error) {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return Job{}, errJobsClosed
	}
	if s.storageRecoveryError != "" && kind != "storage-recovery" {
		err := fmt.Errorf("环境存储待恢复，请在设置中恢复后继续。%s", s.storageRecoveryError)
		s.mu.Unlock()
		return Job{}, err
	}
	var afterSuccess func() error
	if kind == "logout" {
		kind = "stop"
		afterSuccess = func() error { return s.account.lockAccess(true) }
	}
	intentFailed := false
	job, err := s.jobs.submit(&s.op, kind, s.safety.pauseVersion, func() error {
		if kind == "safety" || (s.macosGuest == nil && (kind == "start" || kind == "restore")) {
			if err := s.saveNetworkIntentLocked(false); err != nil {
				intentFailed = true
				return err
			}
		}
		if s.monitorCancel != nil {
			s.monitorCancel()
		}
		return nil
	}, afterSuccess, run)
	s.mu.Unlock()
	if intentFailed {
		_ = s.closeProxyGate("网络恢复意图无法保存，外网保持关闭")
	}
	return job, err
}

func (s *Server) setMessage(message string) { s.jobs.execution().message(message) }

func (s *Server) operationContext() context.Context {
	execution := s.jobs.execution()
	if execution.id != "" {
		return execution.ctx
	}
	return s.lifetimeContext()
}
