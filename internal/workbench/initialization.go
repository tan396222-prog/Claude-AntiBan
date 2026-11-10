package workbench

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/guestos"
)

type initializationRecord struct {
	Version   int  `json:"version"`
	Completed bool `json:"completed"`
	Started   bool `json:"started,omitempty"`
}

type initializationView struct {
	Required bool `json:"required"`
	Started  bool `json:"started"`
	Tracked  bool `json:"tracked"`
	Prepared bool `json:"prepared"`
}

func (s *Server) initializationState() (initializationView, error) {
	if !s.localVMStorage() || s.storageRecoveryStatus().Required {
		return initializationView{}, nil
	}
	data, err := os.ReadFile(filepath.Join(s.profile, "initialization.json"))
	if err == nil {
		var record initializationRecord
		if json.Unmarshal(data, &record) != nil || record.Version != 1 {
			return initializationView{}, errors.New("初始化记录无法读取，请保留现有数据并导出诊断包")
		}
		return initializationView{Required: !record.Completed || s.macSetupPending(), Started: record.Started, Tracked: true, Prepared: s.environmentPrepared()}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return initializationView{}, err
	}
	// Existing installations predate this screen. A directory or partial disk
	// alone is not proof of a completed Linux installation.
	pending, err := guestos.LoadInstallation(s.profile)
	if err != nil {
		return initializationView{}, err
	}
	if s.macosGuest != nil || s.windowsGuest != nil {
		return initializationView{Required: s.macSetupPending()}, nil
	}
	return initializationView{Required: pending != nil || !s.environmentPrepared(), Started: pending != nil}, nil
}

func (s *Server) finishInitialization() error {
	if s.macSetupPending() {
		return errors.New("请先完成客机的系统权限确认")
	}
	s.mu.Lock()
	ready, gate := s.ready, s.safety.Gate
	s.mu.Unlock()
	if !ready || !s.vmRunning() {
		return errors.New("客机尚未就绪，请继续安装并完成启动检查")
	}
	cfg, err := loadConfig(s.configPath)
	if err != nil {
		return err
	}
	if err := cfg.RequireManagedProxy(s.profile); err != nil {
		return err
	}
	if gate == nil || gate.Mode != cage.GateOpen || !gate.PolicyVerified || gate.Fault || gate.Report == nil || !gate.Until.After(time.Now()) || gate.Configuration != cfg.Fingerprint() || gate.Report.Authorizes(gate.RunID, cfg.Fingerprint(), time.Now()) != nil {
		return errors.New("环境网络检查尚未通过，请在初始化界面重新检查")
	}
	return writePrivate(filepath.Join(s.profile, "initialization.json"), initializationRecord{Version: 1, Completed: true})
}

func (s *Server) initialization(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		state, err := s.initializationState()
		if err != nil {
			requestError(w, err)
			return
		}
		jsonReply(w, state)
		return
	}
	if !s.hasTrustedView(r) || !s.localVMStorage() {
		http.Error(w, "请在本机 Cage 窗口完成初始化", http.StatusForbidden)
		return
	}
	var input struct {
		Action string `json:"action"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if !s.op.TryLock() {
		http.Error(w, "环境操作仍在进行，请等待完成后继续", http.StatusConflict)
		return
	}
	defer s.op.Unlock()
	if s.busy() {
		http.Error(w, "环境操作仍在进行，请等待完成后继续", http.StatusConflict)
		return
	}
	state, err := s.initializationState()
	if err != nil {
		requestError(w, err)
		return
	}
	switch input.Action {
	case "begin":
		if state.Required {
			err = writePrivate(filepath.Join(s.profile, "initialization.json"), initializationRecord{Version: 1, Started: state.Started})
		}
	case "install":
		if state.Required {
			err = writePrivate(filepath.Join(s.profile, "initialization.json"), initializationRecord{Version: 1, Started: true})
		}
	case "complete":
		err = s.finishInitialization()
	default:
		err = errors.New("未知的初始化操作")
	}
	if err != nil {
		requestError(w, err)
		return
	}
	jsonReply(w, map[string]bool{"completed": input.Action == "complete" || !state.Required})
}
