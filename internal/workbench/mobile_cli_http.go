package workbench

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

func (s *Server) mobileCLIRoutes(mux routeRegistrar) {
	// Desktop history follows the selected local or SSH host. Mobile routes
	// remain pinned to the paired computer by the remote gateway.
	mux.HandleFunc("GET /api/cli/sessions", s.mobileSessions)
	mux.HandleFunc("GET /api/cli/sessions/{id}", s.mobileSessionView)
	mux.HandleFunc("POST /api/cli/sessions/{id}/open", s.mobileOpenTerminal)
	mux.HandleFunc("POST /api/cli/sessions/{id}/metadata", s.mobileSessionMetadata)
	mux.HandleFunc("POST /api/cli/sessions/{id}/account", s.mobileSessionAccount)
	mux.HandleFunc("POST /api/cli/sessions/{id}/archive", s.mobileArchiveSession)
	mux.HandleFunc("POST /api/cli/sessions/{id}/restore", s.mobileRestoreSession)
	mux.HandleFunc("POST /api/cli/sessions/{id}/purge", s.mobilePurgeSession)
	mux.HandleFunc("GET /api/mobile/cli/projects", s.mobileProjects)
	mux.HandleFunc("GET /api/mobile/cli/sessions", s.mobileSessions)
	mux.HandleFunc("POST /api/mobile/cli/sessions", s.mobileNewSession)
	mux.HandleFunc("GET /api/mobile/cli/sessions/{id}", s.mobileSessionView)
	mux.HandleFunc("POST /api/mobile/cli/sessions/{id}/media", s.mobileSessionMedia)
	mux.HandleFunc("POST /api/mobile/cli/sessions/{id}/messages", s.mobileMessage)
	mux.HandleFunc("POST /api/mobile/cli/sessions/{id}/stop", s.mobileStop)
	mux.HandleFunc("POST /api/mobile/cli/sessions/{id}/approval", s.mobileApprovalResponse)
	mux.HandleFunc("POST /api/mobile/cli/sessions/{id}/open", s.mobileOpenTerminal)
	mux.HandleFunc("POST /api/mobile/cli/sessions/{id}/metadata", s.mobileSessionMetadata)
	mux.HandleFunc("POST /api/mobile/cli/sessions/{id}/account", s.mobileSessionAccount)
	mux.HandleFunc("POST /api/mobile/cli/sessions/{id}/delete", s.mobileArchiveSession)
	mux.HandleFunc("POST /api/mobile/cli/sessions/{id}/archive", s.mobileArchiveSession)
	mux.HandleFunc("POST /api/mobile/cli/sessions/{id}/restore", s.mobileRestoreSession)
	mux.HandleFunc("POST /api/mobile/cli/sessions/{id}/purge", s.mobilePurgeSession)
}

func (s *Server) mobileOpenTerminal(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Automatic bool `json:"automatic"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	s.mobileConnectMu.Lock()
	defer s.mobileConnectMu.Unlock()
	view, ok := s.authorizedMobileSession(w, r)
	if !ok {
		return
	}
	if !s.isTerminalConversation(view.ID) {
		if err := s.promoteMobileConversation(r.Context(), view.ID); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
	}
	s.mobileCLI.mu.Lock()
	record, err := s.mobileCLI.record(s.profile, view.ID)
	s.mobileCLI.mu.Unlock()
	if err != nil {
		http.Error(w, "无法读取会话", 500)
		return
	}
	existing := s.terminals.get(record.TerminalID)
	needsTerminal := existing == nil || !existing.ownsConversation()
	if needsTerminal {
		s.mu.Lock()
		launcher := claudeTerminalCommand(s.preferences)
		s.mu.Unlock()
		if !sharedCLILauncher(launcher) || record.Launcher != "" && launcher != record.Launcher {
			http.Error(w, "主机启动命令与原会话不一致，请恢复原配置后重试", 422)
			return
		}
	}
	if (record.Resume || record.Connection != "") && needsTerminal {
		var state struct {
			Running      bool   `json:"running"`
			ChannelReady bool   `json:"channel_ready"`
			Error        string `json:"error"`
		}
		mode := "active"
		if input.Automatic {
			if err := s.terminals.canStart(); err != nil {
				http.Error(w, err.Error(), 409)
				return
			}
			mode = "handoff"
		}
		query := map[string]any{"cli_id": record.CLIID, "config": record.GuestConfig, "workspace": record.GuestWorkspace}
		if err := s.windowsConversationInput(record, query); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if err := s.runCLIChannel(r.Context(), mode, query, &state); err != nil {
			http.Error(w, err.Error(), 503)
			return
		}
		if state.Error != "" {
			http.Error(w, state.Error, 409)
			return
		}
		if state.Running {
			if input.Automatic {
				if state.ChannelReady {
					var result struct {
						OK bool `json:"ok"`
					}
					resume := map[string]any{"cli_id": record.CLIID, "connection": record.Connection, "action": "resume"}
					if err := s.windowsConversationInput(record, resume); err != nil {
						http.Error(w, err.Error(), http.StatusConflict)
						return
					}
					if err := s.runCLIChannel(r.Context(), "rpc", resume, &result); err != nil || !result.OK {
						http.Error(w, "会话恢复暂未完成，请重试", 503)
						return
					}
				}
				view.ConnectionWaiting = true
				jsonReply(w, view)
				return
			}
			http.Error(w, "原会话仍在电脑终端运行，请先退出其中的 Claude Code，再接续同一段历史", 409)
			return
		}
	}
	terminal, err := s.createTerminal(terminalRequest{Workspace: view.Workspace, Kind: "claude", sharedID: view.ID, requestContext: r.Context()})
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	view.DesktopTerminalID = terminal.id
	if input.Automatic {
		view, err = s.terminalConversationView(r.Context(), view.ID)
		if err != nil {
			http.Error(w, err.Error(), 503)
			return
		}
	}
	jsonReply(w, view)
}

func (s *Server) mobileApprovalResponse(w http.ResponseWriter, r *http.Request) {
	view, ok := s.authorizedMobileSession(w, r)
	if !ok {
		return
	}
	var input struct {
		TurnID     string `json:"request_id"`
		ApprovalID string `json:"approval_id"`
		Allow      *bool  `json:"allow"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if !mobileID(input.TurnID) || !mobileID(input.ApprovalID) || input.Allow == nil {
		http.Error(w, "需要本次操作的完整确认", 400)
		return
	}
	if s.isTerminalConversation(view.ID) {
		if len(view.Turns) == 0 || view.Turns[len(view.Turns)-1].ID != input.TurnID {
			http.Error(w, "审批轮次已变化，请重新读取会话", 409)
			return
		}
		result, err := s.terminalConversationAction(r.Context(), view.ID, map[string]any{"action": "approve", "approval_id": input.ApprovalID, "allow": *input.Allow})
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		jsonReply(w, result)
		return
	}
	// Serialize the actual decision with explicit account locking. Do not
	// retain either mutex while writing the HTTP response.
	s.account.mu.Lock()
	if !s.mobileAuthorized(r.Context()) {
		s.account.mu.Unlock()
		http.Error(w, "请先解锁 Cage", 423)
		return
	}
	m := &s.mobileCLI
	m.mu.Lock()
	run := m.active[view.ID]
	if run != nil && run.pending != nil && run.pending.ctx.Err() == nil {
		turn := &run.record.Turns[len(run.record.Turns)-1]
		if turn.ID == input.TurnID && turn.Status == "running" && turn.Approval != nil && turn.Approval.ID == input.ApprovalID {
			run.pending.reply <- *input.Allow
			run.pending = nil
			turn.Approval = nil
			run.record.changed()
			view = run.record.snapshot()
			m.mu.Unlock()
			s.account.mu.Unlock()
			jsonReply(w, view)
			return
		}
	}
	m.mu.Unlock()
	s.account.mu.Unlock()
	http.Error(w, "这项请求已结束或由其他设备处理，请重新读取会话", 409)
}
func (s *Server) mobileProjects(w http.ResponseWriter, r *http.Request) {
	if s.windowsGuest != nil {
		s.refreshWindowsComponents()
	}
	paths, err := s.projectPaths()
	if err != nil {
		http.Error(w, "无法读取已授权的项目", 500)
		return
	}
	var projects = []map[string]string{}
	for _, path := range paths {
		projects = append(projects, map[string]string{"path": path, "name": filepath.Base(path)})
	}
	s.mu.Lock()
	ready := s.ready && !s.closing
	shared := sharedCLILauncher(claudeTerminalCommand(s.preferences))
	if s.windowsGuest != nil {
		available := s.windowsCapabilitiesLocked()["available"].(map[string]bool)
		shared = shared && available["cli"] && available["terminal"] && available["projects"]
	}
	s.mu.Unlock()
	name, _ := os.Hostname()
	jsonReply(w, map[string]any{"projects": projects, "ready": ready, "host": name, "shared_sessions": shared})
}
func (s *Server) mobileSessions(w http.ResponseWriter, r *http.Request) {
	paths, err := s.projectPaths()
	if err != nil {
		http.Error(w, "无法读取已授权的项目", 500)
		return
	}
	granted := map[string]bool{}
	for _, path := range paths {
		granted[path] = true
	}
	archived := r.URL.Query().Get("archived") == "true"
	sessions, err := s.mobileCLI.summaries(s.profile, granted, archived)
	if err != nil {
		http.Error(w, "无法读取主机上的 CLI 会话记录", 500)
		return
	}
	var notice string
	if err := s.discoverTerminalConversations(r.Context(), paths); err != nil {
		notice = err.Error()
	} else {
		sessions, err = s.mobileCLI.summaries(s.profile, granted, archived)
		if err != nil {
			http.Error(w, "无法读取主机上的 CLI 会话记录", 500)
			return
		}
	}
	s.mu.Lock()
	ready := s.ready && !s.closing
	s.mu.Unlock()
	if !ready || notice != "" {
		for i := range sessions {
			sessions[i].Active = false
		}
	}
	jsonReply(w, map[string]any{"sessions": sessions, "notice": notice})
}
func (s *Server) mobileNewSession(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID        string `json:"id"`
		Workspace string `json:"workspace"`
		Shared    bool   `json:"shared"` // Accepted for old clients; the host always creates a common session.
	}
	if !readJSON(w, r, &input) {
		return
	}
	if !mobileID(input.ID) || input.Workspace == "" {
		http.Error(w, "需要项目和有效的创建编号", 400)
		return
	}
	workspace, err := s.projectWorkspace(input.Workspace)
	if err != nil {
		http.Error(w, "项目未授权或已不可用", 403)
		return
	}
	s.mu.Lock()
	shared := sharedCLILauncher(claudeTerminalCommand(s.preferences))
	s.mu.Unlock()
	if !shared {
		http.Error(w, "当前启动命令无法连接手机，请在主机使用 claude 或 ccs 配置名", 409)
		return
	}
	s.account.mu.Lock()
	if !s.mobileAuthorized(r.Context()) {
		s.account.mu.Unlock()
		http.Error(w, errMobileLocked.Error(), 423)
		return
	}
	view, err := s.mobileCLI.create(s.profile, workspace, input.ID)
	s.account.mu.Unlock()
	if err != nil {
		http.Error(w, "无法创建会话，请检查编号、磁盘和目录权限", 409)
		return
	}
	// Older phones do not send shared. The host chooses the common session
	// implementation, so updating only the computer still fixes new sessions.
	if len(view.Turns) == 0 {
		if _, err := s.createTerminal(terminalRequest{Workspace: workspace, Kind: "claude", sharedID: view.ID, requestContext: r.Context()}); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		s.mobileCLI.mu.Lock()
		record, err := s.mobileCLI.record(s.profile, view.ID)
		if err == nil {
			view = record.snapshot()
		}
		s.mobileCLI.mu.Unlock()
	}
	jsonReply(w, view)
}
func (s *Server) authorizedMobileSession(w http.ResponseWriter, r *http.Request) (mobileSession, bool) {
	view, terminal, ok := s.authorizedMobileSessionRecord(w, r)
	if !ok {
		return view, false
	}
	if view.Archived {
		http.Error(w, "此会话已归档，请先在设置中的已归档会话撤销归档", http.StatusGone)
		return view, false
	}
	if terminal {
		var err error
		view, err = s.terminalConversationView(r.Context(), view.ID)
		if err != nil {
			http.Error(w, err.Error(), 503)
			return view, false
		}
	}
	return view, true
}

// List operations need only the host record; an ended guest terminal need not
// be reachable to rename, pin, or remove it.
func (s *Server) authorizedMobileSessionRecord(w http.ResponseWriter, r *http.Request) (mobileSession, bool, bool) {
	id := r.PathValue("id")
	m := &s.mobileCLI
	m.mu.Lock()
	record, err := m.record(s.profile, id)
	var view mobileSession
	if err == nil {
		if record.Deleted {
			err = os.ErrNotExist
		}
		view = record.snapshot()
	}
	m.mu.Unlock()
	if errors.Is(err, os.ErrNotExist) {
		http.NotFound(w, r)
		return view, false, false
	}
	if err != nil {
		http.Error(w, "无法读取主机上的 CLI 会话记录", 500)
		return view, false, false
	}
	if _, err := s.projectWorkspace(view.Workspace); err != nil {
		http.Error(w, "项目未授权或已不可用", 403)
		return view, false, false
	}
	return view, record.Terminal, true
}
func (s *Server) mobileSessionView(w http.ResponseWriter, r *http.Request) {
	view, ok := s.authorizedMobileSession(w, r)
	if !ok {
		return
	}
	if value := r.URL.Query().Get("after"); value != "" {
		revision, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			http.Error(w, "会话版本无效", 400)
			return
		}
		epoch := r.URL.Query().Get("epoch")
		if epoch != "" && !mobileID(epoch) {
			http.Error(w, "服务标识无效", 400)
			return
		}
		if revision == view.Revision && (epoch == "" || epoch == view.Generation) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	jsonReply(w, view)
}
func (s *Server) mobileMessage(w http.ResponseWriter, r *http.Request) {
	view, ok := s.authorizedMobileSession(w, r)
	if !ok {
		return
	}
	var input struct {
		ID   string `json:"request_id"`
		Text string `json:"text"`
	}
	if !readJSON(w, r, &input, 72<<10) {
		return
	}
	if !mobileID(input.ID) || strings.TrimSpace(input.Text) == "" || len(input.Text) > mobilePromptLimit || !utf8.ValidString(input.Text) {
		http.Error(w, "需要有效的发送编号和 32 KiB 以内的消息", 400)
		return
	}
	if s.isTerminalConversation(view.ID) {
		result, err := s.terminalConversationAction(r.Context(), view.ID, map[string]any{"action": "send", "request_id": input.ID, "text": input.Text})
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		jsonReply(w, result)
		return
	}
	result, err := s.submitMobileTurn(r.Context(), view.ID, input.ID, input.Text)
	if err != nil {
		if errors.Is(err, errTerminalBusy) {
			http.Error(w, "主机正在处理其他请求，请保留发送编号重试", http.StatusServiceUnavailable)
			return
		}
		if errors.Is(err, errMobileLocked) {
			http.Error(w, err.Error(), http.StatusLocked)
			return
		}
		http.Error(w, err.Error(), 409)
		return
	}
	jsonReply(w, result)
}
func (s *Server) mobileStop(w http.ResponseWriter, r *http.Request) {
	view, ok := s.authorizedMobileSession(w, r)
	if !ok {
		return
	}
	var input struct {
		ID string `json:"request_id"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if !mobileID(input.ID) {
		http.Error(w, "执行编号无效", 400)
		return
	}
	if s.isTerminalConversation(view.ID) {
		result, err := s.stopTerminalConversation(r.Context(), view, input.ID)
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		jsonReply(w, result)
		return
	}
	s.account.mu.Lock()
	if !s.mobileAuthorized(r.Context()) {
		s.account.mu.Unlock()
		http.Error(w, errMobileLocked.Error(), http.StatusLocked)
		return
	}
	result, err := s.stopMobileTurn(view.ID, input.ID)
	s.account.mu.Unlock()
	if err != nil {
		http.Error(w, "未找到这次执行", 404)
		return
	}
	jsonReply(w, result)
}
