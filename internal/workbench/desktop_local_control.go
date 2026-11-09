package workbench

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"sort"
)

// The launcher authenticates these requests with the private controller record.
// The browser-facing gateway never forwards this endpoint or exposes its token.
type desktopLocalRequest struct {
	Token        string `json:"token"`
	ControllerID string `json:"controller_id,omitempty"`
	Stop         bool   `json:"stop,omitempty"`
	Running      bool   `json:"running,omitempty"`
	Terminals    int    `json:"terminals,omitempty"`
	JobID        string `json:"job_id,omitempty"`
}

type desktopLocalReply struct {
	DesktopLocalStatus
	Identity   ControllerIdentity `json:"identity"`
	OwnerID    string             `json:"owner_id"`
	InstanceID string             `json:"instance_id"`
	LaunchID   string             `json:"launch_id,omitempty"`
	JobID      string             `json:"job_id,omitempty"`
	JobRunning bool               `json:"job_running,omitempty"`
	JobFailed  bool               `json:"job_failed,omitempty"`
}

func localControllerID(token string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte("desktop-local\x00"+token)))
}

func (s *Server) desktopLocalSnapshot() desktopLocalReply {
	job := s.jobs.snapshot()
	reply := desktopLocalReply{
		DesktopLocalStatus: DesktopLocalStatus{Available: true, ServiceRunning: true},
		Identity:           s.identity, OwnerID: localControllerID(s.token), InstanceID: s.instanceID,
		LaunchID: os.Getenv("CAGE_DESKTOP_START_ID"), JobID: job.ID, JobRunning: job.Running, JobFailed: job.Error != "",
	}
	owners, err := controllerVMOwners(s.profile)
	reply.Running = len(owners) != 0
	if err != nil {
		reply.Message = "本机环境状态待确认"
		return reply
	}
	reply.Known = true
	var terminals []string
	for _, session := range s.terminals.snapshot() {
		if session["closed"] == false {
			reply.LiveTerminals++
			id, _ := session["id"].(string)
			terminals = append(terminals, id)
		}
	}
	s.mu.Lock()
	reply.Busy = s.closing || job.Running
	s.mu.Unlock()
	if s.remote != nil && s.remote.isRemote() {
		reply.Busy = true
	}
	if reply.Busy {
		reply.Message = "本机控制服务正在执行操作或被其他入口使用"
	}
	// The same owner can acquire new work while the confirmation dialog is
	// open. Bind its public stop confirmation to the observed workload too.
	sort.Strings(terminals)
	reply.ControllerID = fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%q\x00%s\x00%t\x00%q", reply.OwnerID, owners, job.ID, reply.Busy, terminals))))
	return reply
}

func (s *Server) desktopLocalControl(w http.ResponseWriter, r *http.Request) {
	var request desktopLocalRequest
	if !readJSON(w, r, &request) {
		return
	}
	if s.clientOnly || !equalSecret(request.Token, s.token) {
		http.Error(w, "Launcher refused", http.StatusUnauthorized)
		return
	}
	switch r.PathValue("action") {
	case "status":
		jsonReply(w, s.desktopLocalSnapshot())
	case "release":
		if !s.op.TryLock() {
			http.Error(w, "本机正在执行操作，请稍后重试", http.StatusConflict)
			return
		}
		view := s.desktopLocalSnapshot()
		if !view.Known || view.Busy || request.ControllerID != view.ControllerID || request.Running != view.Running || request.Terminals != view.LiveTerminals || (request.JobID != "" && request.JobID != view.JobID) {
			s.op.Unlock()
			http.Error(w, "本机任务或控制服务已变化，请重新确认", http.StatusConflict)
			return
		}
		if !view.Running && view.LiveTerminals == 0 {
			s.handoffLocked(w)
			return
		}
		defer s.op.Unlock()
		if !request.Stop {
			http.Error(w, "本机环境仍在运行", http.StatusConflict)
			return
		}
		if _, unlocked := s.account.access(); !unlocked {
			http.Error(w, "请先返回本机解锁，再停止本机环境", http.StatusLocked)
			return
		}
		// This stop may not cancel another admitted operation. Check and submit
		// under the same admission lock used by submitJob, then let the existing
		// job manager execute stopEnvironment under the operation lock.
		s.mu.Lock()
		if s.closing || s.jobs.snapshot().Running {
			s.mu.Unlock()
			http.Error(w, "本机正在执行操作，请稍后重试", http.StatusConflict)
			return
		}
		job, err := s.jobs.submit(&s.op, "stop", s.safety.pauseVersion, nil, nil, func() error {
			_ = s.pauseNetworkNow("正在停止本机环境")
			return s.stopEnvironment()
		})
		s.mu.Unlock()
		if err != nil {
			requestError(w, err)
			return
		}
		jsonReply(w, desktopLocalReply{JobID: job.ID, JobRunning: true})
	default:
		http.NotFound(w, r)
	}
}
