package workbench

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/sylvinus/agent-vm/internal/gitguard"
	"github.com/sylvinus/agent-vm/internal/gitx"
)

type hostGitState struct {
	State     string `json:"state"`
	Ready     bool   `json:"ready"`
	CanEnable bool   `json:"can_enable"`
	Message   string `json:"message"`
}

func inspectHostGit(ctx context.Context) hostGitState {
	state := gitguard.BareState(ctx)
	view := hostGitState{State: state}
	switch state {
	case gitguard.BareOK:
		view.Ready = true
		view.Message = "本机 Git 安全保护已启用。"
	case gitguard.BareNoGit:
		view.Ready = true
		view.Message = "本机未安装可用的 Git，无需修改；以后启动环境时会重新检查。"
	case gitguard.BareUnset:
		view.CanEnable = true
		view.Message = "启动前需要启用本机 Git 安全保护。"
	case gitguard.BareOld:
		view.Message = "本机 Git 版本过旧，请升级到 2.38 或更新版本后重新检查。"
	default:
		view.Message = "暂时无法检查本机 Git。请确认 Git 安装完整，再重新检查；Cage 不会自动打开安装器。"
	}
	return view
}

func (s *Server) hostGit(w http.ResponseWriter, r *http.Request) {
	jsonReply(w, inspectHostGit(r.Context()))
}

func (s *Server) enableHostGit(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Confirm bool `json:"confirm"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if !input.Confirm {
		http.Error(w, "请先确认启用本机 Git 安全保护。", http.StatusBadRequest)
		return
	}
	if !s.op.TryLock() {
		http.Error(w, "正在处理环境操作，请完成后重试。", http.StatusConflict)
		return
	}
	defer s.op.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	before := inspectHostGit(ctx)
	if before.Ready {
		jsonReply(w, before)
		return
	}
	if !before.CanEnable {
		http.Error(w, before.Message, http.StatusConflict)
		return
	}
	cmd := gitx.Untrusted(ctx, "config", "--global", "--replace-all", "safe.bareRepository", "explicit")
	cmd.Dir = s.profile
	if err := cmd.Run(); err != nil {
		http.Error(w, "未能保存 Git 安全设置。请检查全局 Git 配置的写入权限，关闭其他 Git 设置操作后重试。", http.StatusInternalServerError)
		return
	}
	after := inspectHostGit(ctx)
	if !after.Ready {
		http.Error(w, "Git 设置已写入，但检查尚未通过。请检查全局 Git 配置后重新检测。", http.StatusConflict)
		return
	}
	jsonReply(w, after)
}

func requireHostGit(ctx context.Context) error {
	state := inspectHostGit(ctx)
	if !state.Ready {
		return errors.New(state.Message + " 请从工作台点击启动，按提示完成检查。")
	}
	return nil
}
