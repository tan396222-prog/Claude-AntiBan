package workbench

import (
	"context"
	_ "embed"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/cliconversation"
)

//go:embed mobile_cli_supervisor.py
var mobileSupervisor string

var mobileCCSProfile = regexp.MustCompile("^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$")

type mobileEpochKey struct{}

var errMobileLocked = errors.New("Cage 已锁定，请重新解锁后操作")

// Caller holds the account mutex. An operation begun under an old unlock
// must not gain authority from a later unlock of the same account.
func (s *Server) mobileAuthorized(ctx context.Context) bool {
	epoch, _ := ctx.Value(mobileEpochKey{}).(context.Context)
	return len(s.account.key) == 32 && epoch != nil && epoch == s.account.epoch && epoch.Err() == nil && ctx.Err() == nil
}

func mobileLaunchArgs(launcher string, account claudeSelection) ([]string, error) {
	parts := strings.Fields(launcher)
	if len(parts) == 2 && parts[0] == "ccs" && mobileCCSProfile.MatchString(parts[1]) {
		if account.ID != "" {
			return nil, errors.New("当前账号池需要直接使用 claude 启动命令")
		}
		return parts, nil
	}
	if len(parts) != 1 || parts[0] != "claude" {
		return nil, errors.New("移动会话支持 claude 或 ccs 配置名，请在主机检查 Claude 启动命令")
	}
	if account.ID == "" && account.GuestID == "" {
		return parts, nil
	}
	boundDefault := account.ID == "" && account.GuestID == "default"
	if (!boundDefault && !validClaudeID(account.ID)) || !validClaudeGuestID(account.GuestID) || len(account.Subject) != 64 {
		return nil, errors.New("Claude 账号身份无效")
	}
	return []string{"env", "CAGE_CLAUDE_SUBJECT=" + account.Subject, "/usr/local/bin/cage-claude-account", "run", account.GuestID}, nil
}

func sharedCLILauncher(launcher string) bool {
	_, err := mobileLaunchArgs(launcher, claudeSelection{})
	return err == nil
}

func (s *Server) mobileRunner(prefix []string) cliconversation.Runner {
	return cliconversation.Runner{SupervisedStdin: true, Command: func(ctx context.Context, workspace string, args []string) (*exec.Cmd, error) {
		// The login shell supplies the guest's PATH and Claude provider configuration.
		// Shell startup output goes to stderr so stdout contains only CLI JSON.
		login := `exec 3>&1 1>&2; exec /bin/bash -lc ` + shellescape.Quote(`exec 1>&3 3>&-; cd -- "$1" && shift && exec "$@"`) + ` "$0" "$@"`
		argv := []string{"/usr/bin/python3", "-I", "-c", mobileSupervisor, guestPath(workspace), "/bin/bash", "-c",
			login, "cage-mobile-cli", guestPath(workspace)}
		argv = append(argv, mobileIdentityCommand(prefix, args)...)
		command := "exec " + shellescape.QuoteCommand(argv)
		return s.sshCommand(ctx, "-T", s.sshTarget(), "--", command), nil
	}}
}

// prepareMobileLaunch runs with the environment operation lock held. The mobile
// request selects only a previously granted workspace. Account identity and the
// launcher are fixed on the first accepted turn, including subsequent resumes.
func (s *Server) prepareMobileLaunch(record *mobileRecord) (claudeSelection, string, cliconversation.Runner, error) {
	var empty cliconversation.Runner
	if s.windowsGuest != nil {
		return claudeSelection{}, "", empty, errWindowsLegacyMobile
	}
	s.mu.Lock()
	ready, closing, update, configured := s.ready, s.closing, s.guestUpdateRequired, claudeTerminalCommand(s.preferences)
	s.mu.Unlock()
	if closing || !ready || !s.vmRunning() || s.busy() {
		return claudeSelection{}, "", empty, errTerminalNotReady
	}
	if _, err := s.connectProject(record.Workspace, false); err != nil {
		return claudeSelection{}, "", empty, err
	}
	selection, launcher := record.Account, record.Launcher
	if launcher == "" {
		launcher = configured
		var err error
		selection, err = s.selectTerminalClaudeAccount(launcher, "", false, record.Workspace)
		if err != nil {
			return selection, "", empty, err
		}
		// An existing default subscription can use the same guest-side subject
		// check as a registered account, without enrolling it in scheduling.
		if launcher == "claude" && selection.ID == "" && !update {
			if epoch, ok := s.account.access(); ok {
				if status, _, err := s.inspectClaudeDefault(epoch, true); err == nil && status.subject() != "" {
					selection = claudeSelection{GuestID: "default", Subject: status.subject()}
				}
			}
		}
	} else if launcher != configured {
		return selection, "", empty, errors.New("主机已更改 Claude 启动命令。请恢复原配置或新建会话")
	}
	if selection.ID != "" {
		if update {
			return selection, "", empty, errGuestUpdateRequired
		}
		s.claudeAccounts.mu.Lock()
		list, err := s.loadClaudeAccounts()
		s.claudeAccounts.mu.Unlock()
		if err != nil {
			return selection, "", empty, err
		}
		account := list.find(selection.ID)
		if account == nil || !account.Enabled || account.Status != "ready" || account.guestID() != selection.GuestID || claudeIdentityHash(account.Email, account.Organization) != selection.Subject {
			return selection, "", empty, errors.New("原会话的 Claude 账号已停用或发生变化")
		}
		if _, err = s.terminals.existingClaudeLogin(selection.ID, false); err != nil {
			return selection, "", empty, err
		}
	}
	prefix, err := mobileLaunchArgs(launcher, selection)
	return selection, launcher, s.mobileRunner(prefix), err
}

func findMobileTurn(record *mobileRecord, id string) *mobileTurn {
	for i := range record.Turns {
		if record.Turns[i].ID == id {
			return &record.Turns[i]
		}
	}
	return nil
}
func (s *Server) submitMobileTurn(ctx context.Context, id, requestID, prompt string) (mobileSession, error) {
	var empty mobileSession
	if !s.op.TryLock() {
		return empty, errTerminalBusy
	}
	defer s.op.Unlock()
	m := &s.mobileCLI
	m.mu.Lock()
	record, err := m.record(s.profile, id)
	if err != nil {
		m.mu.Unlock()
		return empty, err
	}
	if record.Deleted {
		m.mu.Unlock()
		return empty, os.ErrNotExist
	}
	if record.Archived {
		m.mu.Unlock()
		return empty, errors.New("此会话已归档，请先撤销归档")
	}
	if previous := findMobileTurn(record, requestID); previous != nil {
		view := record.snapshot()
		m.mu.Unlock()
		if previous.Prompt != prompt {
			return empty, errors.New("同一发送编号已用于另一条消息")
		}
		return view, nil
	}
	if m.active[id] != nil || len(m.active) >= 4 {
		m.mu.Unlock()
		return empty, errors.New("已有轮次在运行，请等待或停止后重试")
	}
	size := len(prompt) + mobileReplyLimit
	if record.SingleTurn {
		m.mu.Unlock()
		return empty, errors.New("此配置只能进行单轮对话，请新建会话")
	}
	for _, t := range record.Turns {
		size += len(t.Prompt) + len(t.Text)
		for _, tool := range t.Tools {
			size += len(tool.ID) + len(tool.Name) + len(tool.Detail) + 64
		}
	}
	if size > mobileHistoryLimit || len(record.Turns) >= 256 {
		m.mu.Unlock()
		return empty, errors.New("会话已达到移动历史容量，请新建会话继续")
	}
	m.mu.Unlock()

	selection, launcher, runner, err := s.prepareMobileLaunch(record)
	if err != nil {
		return empty, err
	}
	return s.admitMobileTurn(ctx, record, requestID, prompt, selection, launcher, runner)
}

// Called under the operation lock. A disconnected viewer only stops polling.
// The accepted process belongs to the service and environment lifecycle.
func (s *Server) admitMobileTurn(request context.Context, record *mobileRecord, requestID, prompt string, selection claudeSelection, launcher string, runner cliconversation.Runner) (mobileSession, error) {
	var empty mobileSession
	if err := request.Err(); err != nil {
		return empty, err
	}
	parent, finish, err := s.beginActivity(context.Background())
	if err != nil {
		return empty, err
	}
	ctx, cancel := context.WithCancel(parent)
	m := &s.mobileCLI
	s.account.mu.Lock()
	if !s.mobileAuthorized(request) {
		s.account.mu.Unlock()
		cancel()
		finish()
		return empty, errMobileLocked
	}
	m.mu.Lock()
	record.Account, record.Launcher = selection, launcher
	resume := record.Resume
	// If the service exits before receiving a result, only try resuming the
	// assigned CLI UUID. Do not create and rerun a new conversation silently.
	record.Resume = true
	if len(record.Turns) == 0 {
		title := []rune(strings.Join(strings.Fields(prompt), " "))
		record.Title = string(title[:min(len(title), 40)])
	}
	record.Turns = append(record.Turns, mobileTurn{ID: requestID, Prompt: prompt, Status: "running"})
	record.changed()
	if err = writePrivate(mobileRecordPath(s.profile, record.ID), record); err != nil {
		m.mu.Unlock()
		s.account.mu.Unlock()
		cancel()
		finish()
		return empty, errors.New("无法保存消息，未启动执行")
	}
	run := &mobileRun{record: record, cancel: cancel, done: make(chan struct{}), approvalGate: make(chan struct{}, 1)}
	if m.active == nil {
		m.active = make(map[string]*mobileRun)
	}
	m.active[record.ID] = run
	view := record.snapshot()
	m.mu.Unlock()
	s.account.mu.Unlock()
	if selection.ID != "" {
		s.noteClaudeSelection(selection.ID, record.Workspace)
	}
	go func() {
		defer finish()
		defer close(run.done)
		defer cancel()
		turn := &record.Turns[len(record.Turns)-1]
		runner.Approve = func(ctx context.Context, request cliconversation.PermissionRequest) bool {
			return m.awaitMobileDecision(ctx, run, turn, request)
		}
		runner.OnTools = func(tools []cliconversation.ToolActivity) {
			m.mu.Lock()
			defer m.mu.Unlock()
			turn.Tools = tools
			record.changed()
		}
		result, runErr := runner.Run(ctx, cliconversation.Turn{Workspace: record.Workspace, SessionID: record.CLIID, Prompt: prompt, Resume: resume}, func(delta string) {
			m.mu.Lock()
			defer m.mu.Unlock()
			if turn.Status != "running" || turn.Truncated || delta == "" {
				return
			}
			remain := mobileReplyLimit - len(turn.Text)
			part := mobileTrim(delta, remain)
			turn.Text += part
			turn.Truncated = len(part) < len(delta)
			record.changed()
		})
		m.mu.Lock()
		defer m.mu.Unlock()
		record.Resume = resume || result.Started
		record.SingleTurn = result.SingleTurn && result.Started
		turn.Approval = nil
		run.pending = nil
		switch {
		case ctx.Err() != nil || turn.Status == "stopping":
			turn.Status = "stopped"
		case runErr != nil:
			turn.Status, turn.Error = "failed", runErr.Error()
		default:
			turn.Status = "completed"
		}
		if result.Text != "" {
			turn.Text = mobileTrim(result.Text, mobileReplyLimit)
			turn.Truncated = len(turn.Text) < len(result.Text)
		}
		turn.DeniedTools = slices.Clone(result.DeniedTools)
		record.changed()
		if err := writePrivate(mobileRecordPath(s.profile, record.ID), record); err != nil {
			// Keep the error and result available in this process. The pre-launch
			// marker remains on disk to prevent replay after a restart.
			turn.Error = "本轮结果未能保存，请检查主机磁盘后重新读取会话"
			turn.Status = "failed"
			run.cancel = nil
		} else {
			delete(m.active, record.ID)
		}
		if m.catalog != nil {
			m.catalog[record.ID] = record.summary()
		}
	}()
	return view, nil
}

func (s *Server) stopMobileTurn(id, requestID string) (mobileSession, error) {
	m := &s.mobileCLI
	m.mu.Lock()
	defer m.mu.Unlock()
	record, err := m.record(s.profile, id)
	if err != nil {
		return mobileSession{}, err
	}
	turn := findMobileTurn(record, requestID)
	if turn == nil {
		return mobileSession{}, os.ErrNotExist
	}
	run := m.active[id]
	if run != nil && run.cancel != nil && turn.Status == "running" && turn == &record.Turns[len(record.Turns)-1] {
		turn.Status = "stopping"
		turn.Approval = nil
		run.pending = nil
		record.changed()
		run.cancel()
	}
	return record.snapshot(), nil
}

func (m *mobileCLIStore) awaitMobileDecision(ctx context.Context, run *mobileRun, turn *mobileTurn, request cliconversation.PermissionRequest) bool {
	// Parallel tool calls retain their own identities. Expose one complete
	// request at a time so an answer cannot migrate to a competing operation.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	select {
	case run.approvalGate <- struct{}{}:
	case <-ctx.Done():
		return false
	}
	defer func() { <-run.approvalGate }()
	pending := &mobileDecision{ctx: ctx, reply: make(chan bool, 1)}
	m.mu.Lock()
	if ctx.Err() != nil || turn.Status != "running" {
		m.mu.Unlock()
		return false
	}
	run.pending = pending
	turn.Approval = &mobileApproval{ID: uuid.NewString(), Tool: request.Tool, Input: request.Input}
	run.record.changed()
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if run.pending == pending {
			run.pending = nil
			turn.Approval = nil
			run.record.changed()
		}
	}()
	select {
	case <-ctx.Done():
		return false
	case allow := <-pending.reply:
		return allow && ctx.Err() == nil
	}
}
