package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/sylvinus/agent-vm/internal/containerenv"
	"github.com/sylvinus/agent-vm/internal/managedproxy"
	"github.com/sylvinus/agent-vm/internal/version"
)

const controlProtocol = 1

type ControllerIdentity struct {
	Version  string `json:"version"`
	Build    string `json:"build"`
	Protocol int    `json:"protocol"`
	PID      int    `json:"pid"`
}

func currentController() (ControllerIdentity, error) {
	build, err := version.ExecutableIdentity()
	return ControllerIdentity{Version: version.Version, Build: build, Protocol: controlProtocol, PID: os.Getpid()}, err
}

func (identity ControllerIdentity) matches(other ControllerIdentity) bool {
	return identity.Build != "" && identity.Build == other.Build && identity.Protocol == other.Protocol
}

type launchResponse struct {
	DesktopGateway bool               `json:"desktop_gateway,omitempty"`
	URL            string             `json:"url"`
	Controller     ControllerIdentity `json:"controller"`
}

func (s *Server) launcherAuthenticated(w http.ResponseWriter, r *http.Request) bool {
	var input struct {
		Token string `json:"token"`
	}
	if !readJSON(w, r, &input) {
		return false
	}
	if !equalSecret(input.Token, s.token) {
		http.Error(w, "Launcher refused", http.StatusUnauthorized)
		return false
	}
	return true
}

func (s *Server) handoff(w http.ResponseWriter, r *http.Request) {
	if !s.launcherAuthenticated(w, r) {
		return
	}
	if !s.op.TryLock() {
		http.Error(w, "请先保存工作并停止环境，再完成更新。", http.StatusConflict)
		return
	}
	s.handoffLocked(w)
}

// handoffLocked takes ownership of the operation lock and releases it on all paths.
func (s *Server) handoffLocked(w http.ResponseWriter) {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		s.op.Unlock()
		http.Error(w, "后台正在更新，请重新打开 Cage。", http.StatusServiceUnavailable)
		return
	}
	busy := s.jobs.snapshot().Running || controllerHasVM(s.profile)
	busy = busy || s.terminals.hasLive()
	if busy || s.shutdown == nil {
		s.mu.Unlock()
		s.op.Unlock()
		http.Error(w, "请先保存工作并停止环境，再完成更新。", http.StatusConflict)
		return
	}
	s.closing = true
	s.mu.Unlock()
	if err := managedproxy.Stop(s.profile); err != nil {
		s.mu.Lock()
		s.closing = false
		s.mu.Unlock()
		s.op.Unlock()
		http.Error(w, "代理尚未停止，未继续后台更新。", http.StatusConflict)
		return
	}
	// A fixed length keeps the complete acknowledgement readable even if
	// cancellation prevents net/http from writing a final chunk afterwards.
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", "18")
	_, _ = w.Write([]byte("{\"stopping\":true}\n"))
	// Publish the acknowledgement before cancelling this request. Admission is
	// already sealed; shutdown must not wait for requests while holding op.
	_ = http.NewResponseController(w).Flush()
	s.op.Unlock()
	go s.shutdown()
}

func controllerHasVM(profile string) bool {
	running, err := controllerVMState(profile)
	return err != nil || running
}

// Callers that display a state must distinguish a missing VM from a failed probe.
func controllerVMState(profile string) (bool, error) {
	owners, err := controllerVMOwners(profile)
	return len(owners) != 0, err
}

func controllerVMOwners(profile string) ([]string, error) {
	if containerenv.Configured(profile) {
		environment, err := containerenv.Load(profile)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		state, err := environment.State(ctx)
		if err != nil || !state.Running {
			return nil, err
		}
		return []string{fmt.Sprintf("%s/%d", state.ContainerID, state.PID)}, nil
	}
	records, err := filepath.Glob(filepath.Join(profile, "state", "lima", "*", "cage-running.json"))
	if err != nil {
		return nil, err
	}
	var owners []string
	for _, path := range records {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var record struct {
			PID int `json:"pid"`
		}
		if json.Unmarshal(b, &record) != nil || record.PID <= 0 {
			return nil, errors.New("环境进程记录无效")
		}
		if processExists(record.PID) {
			owners = append(owners, fmt.Sprintf("%s/%d", filepath.Base(filepath.Dir(path)), record.PID))
		}
	}
	return owners, nil
}
