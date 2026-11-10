package workbench

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/diagnostics"
)

//go:embed cli_channel.py
var cliChannelHelper string

//go:embed cli_channel.resume.md
var cliResumePrompt string

//go:embed claude_account_resume.md
var claudeAccountResumePrompt string

var cliChannelEncoded = func() string {
	var helper strings.Builder
	for _, line := range strings.Split(cliChannelHelper, "\n") {
		if strings.TrimSpace(line) != "" && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			helper.WriteString(line + "\n")
		}
	}
	bundle := []byte(strings.Join([]string{helper.String(), cliResumePrompt, claudeAccountResumePrompt}, "\x00"))
	var compressed bytes.Buffer
	writer, _ := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	_, _ = writer.Write(bundle)
	_ = writer.Close()
	return cliTransport85(compressed.Bytes())
}()

// Python's RFC 1924 base85 alphabet has no single quote. It fits the compressed
// helper within Windows' command limit without expanding shell quoting.
func cliTransport85(data []byte) string {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz!#$%&()*+-;<=>?@^_`{|}~"
	var out strings.Builder
	for len(data) > 0 {
		n := min(4, len(data))
		var value uint64
		for i := 0; i < 4; i++ {
			value <<= 8
			if i < n {
				value |= uint64(data[i])
			}
		}
		var block [5]byte
		for i := 4; i >= 0; i-- {
			block[i] = alphabet[value%85]
			value /= 85
		}
		out.Write(block[:n+1])
		data = data[n:]
	}
	return out.String()
}

type terminalConversation struct {
	NativeCCSUnverified bool         `json:"native_ccs_unverified"`
	AccountFailure      string       `json:"account_failure"`
	ResumePending       bool         `json:"resume_pending"`
	NativeHistory       bool         `json:"native_history"`
	CLIID               string       `json:"cli_id"`
	Config              string       `json:"config"`
	Connection          string       `json:"connection"`
	Workspace           string       `json:"workspace"`
	Title               string       `json:"title"`
	UpdatedAt           float64      `json:"updated_at"`
	Turns               []mobileTurn `json:"turns"`
	CanSend             bool         `json:"can_send"`
	ChannelReady        bool         `json:"channel_ready"`
	HasHistory          bool         `json:"has_history"`
	Running             bool         `json:"running"`
	Active              bool         `json:"active"`
	ConnectionNotice    string       `json:"connection_notice"`
	HistoryTruncated    bool         `json:"history_truncated"`
}

func cliChannelCommand(mode, argument string) string {
	// Versioned helpers let an open terminal keep its original protocol while a
	// rebuilt host begins new sessions. No account configuration is overwritten.
	bootstrap := fmt.Sprintf(`import base64, gzip, os, pathlib, sys, tempfile
folder = pathlib.Path.home() / ".local/share/cage/cli-channel-code"
folder.mkdir(parents=True, exist_ok=True, mode=0o700)
name = "%x"
files = gzip.decompress(base64.b85decode("%s")).decode("utf-8").split("\0")
for suffix, content in zip((".py", ".resume.md", ".account-resume.md"), files):
    path = folder / (name + suffix)
    if not path.exists():
        with tempfile.NamedTemporaryFile(dir=folder, delete=False) as stream:
            stream.write(content.encode("utf-8"))
            temporary = stream.name
        os.replace(temporary, path)
path = folder / (name + ".py")
os.execv(sys.executable, [sys.executable, "-I", str(path), *sys.argv[1:]])
`, sha256.Sum256([]byte(cliChannelHelper+cliResumePrompt+claudeAccountResumePrompt)), cliChannelEncoded)
	return shellescape.QuoteCommand([]string{"python3", "-I", "-c", bootstrap, mode, argument})
}

func (s *Server) runCLIChannel(ctx context.Context, mode string, input any, output any) error {
	started := time.Now()
	epoch, ok := s.account.access()
	if !ok {
		return errMobileLocked
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	stop := context.AfterFunc(epoch, cancel)
	defer func() { stop(); cancel() }()
	if s.windowsGuest != nil {
		return s.windowsCLIRequest(ctx, map[string]any{"action": "channel", "mode": mode, "input": input}, output)
	}
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	command := "exec /bin/bash -c " + shellescape.Quote("exec 3>&1 1>&2; exec /bin/bash -lc "+shellescape.Quote("exec 1>&3 3>&-; exec "+s.cliHelperCommand(mode, "-")))
	cmd := s.sshCommand(ctx, "-T", s.sshTarget(), "--", command)
	cmd.Stdin = strings.NewReader(string(data))
	var stderr fileControlStderr
	cmd.Stderr = &stderr
	cmd.WaitDelay = 2 * time.Second
	result, err := cmd.Output()
	if err != nil || len(result) > 4<<20 || json.Unmarshal(result, output) != nil {
		failure := "guest_response"
		message := "电脑未能读取会话状态，聊天记录已保留。"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			failure, message = "timeout", "电脑读取运行环境超时，暂时无法获取会话状态。"
		} else if err != nil {
			failure = "guest_command"
			if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 255 {
				failure, message = "ssh_connection", "电脑暂时无法连接运行环境，聊天记录已保留。"
			}
		}
		exit := -1
		if cmd.ProcessState != nil {
			exit = cmd.ProcessState.ExitCode()
		}
		// Record transport and protocol evidence without terminal output,
		// prompts, account paths or raw SSH/Python error messages.
		_ = diagnostics.New(s.profile).Record("warn", "cli-channel", fmt.Sprintf("operation=%s failure=%s exit=%d elapsed_ms=%d context=%v output_bytes=%d stderr_bytes=%d", mode, failure, exit, time.Since(started).Milliseconds(), ctx.Err(), len(result), len(stderr.data)))
		return errors.New(message)
	}
	return nil
}

func (s *Server) discoverTerminalConversations(ctx context.Context, paths []string) error {
	s.conversationMu.Lock()
	defer s.conversationMu.Unlock()
	s.mu.Lock()
	ready := s.ready && !s.closing
	s.mu.Unlock()
	if !ready {
		return nil
	}
	accounts := []string{"default"}
	s.claudeAccounts.mu.Lock()
	list, err := s.loadClaudeAccounts()
	s.claudeAccounts.mu.Unlock()
	if err != nil {
		return err
	}
	for _, account := range list.Accounts {
		if account.guestID() != "default" {
			accounts = append(accounts, account.guestID())
		}
	}
	workspaces := make(map[string]string, len(paths))
	for _, path := range paths {
		if s.windowsGuest != nil {
			binding, err := s.windowsProjectForHost(path)
			if err == nil {
				workspaces[binding.Path] = path
			}
		} else {
			workspaces[s.cliWorkspace(path)] = path
		}
	}
	var found []terminalConversation
	if err := s.runCLIChannel(ctx, "discover", map[string]any{"accounts": accounts, "workspaces": workspaces}, &found); err != nil {
		return err
	}
	m := &s.mobileCLI
	m.mu.Lock()
	defer m.mu.Unlock()
	known := map[string]*mobileRecord{}
	running := map[string]bool{}
	for id := range m.catalog {
		record, err := m.record(s.profile, id)
		if err != nil {
			return err
		}
		known[record.CLIID] = record
		for _, execution := range record.Executions {
			known[execution.CLIID] = record
		}
	}
	for _, item := range found {
		var workspace, matched string
		for guest, path := range workspaces {
			if cliWorkspaceWithin(item.Workspace, guest, s.windowsGuest != nil) && len(guest) > len(matched) {
				workspace, matched = path, guest
			}
		}
		if workspace == "" || !validCLIUUID(item.CLIID) {
			continue
		}
		record := known[item.CLIID]
		if record != nil && record.CLIID != item.CLIID {
			continue
		}
		if record != nil && (record.Deleted || record.Archived) {
			continue
		}
		if record != nil && !record.Terminal {
			continue
		}
		if record == nil {
			record = &mobileRecord{mobileSession: mobileSession{mobileSummary: mobileSummary{ID: uuid.NewString(), Workspace: workspace}, Turns: []mobileTurn{}}, CLIID: item.CLIID, Terminal: true, Resume: true}
			record.Generation = m.epochLocked()
			record.Account.GuestID = "default"
			for _, account := range list.Accounts {
				config := strings.ReplaceAll(item.Config, "\\", "/")
				if strings.Contains(config, "/claude-accounts/"+account.guestID()+"/") || s.windowsGuest != nil && strings.HasSuffix(strings.ToLower(config), "/accounts/"+account.guestID()+"/config") {
					record.Account = claudeSelection{ID: account.ID, GuestID: account.guestID(), Subject: claudeIdentityHash(account.Email, account.Organization)}
				}
			}
		}
		record.Workspace = workspace
		record.Active = item.Active
		record.Title, record.GuestConfig, record.Connection = item.Title, item.Config, item.Connection
		record.GuestWorkspace = item.Workspace
		if item.Connection != "" {
			if terminal := s.terminals.forConversation(item.Connection); terminal != nil {
				record.TerminalID = terminal.id
				terminal.mu.Lock()
				terminal.sharedID = record.ID
				terminal.mu.Unlock()
			}
		}
		record.UpdatedAt = time.UnixMilli(int64(item.UpdatedAt * 1000)).UTC()
		if err := writePrivate(mobileRecordPath(s.profile, record.ID), record); err != nil {
			return err
		}
		m.catalog[record.ID] = record.summary()
		running[record.ID] = running[record.ID] || item.Running
		known[record.CLIID] = record
	}
	m.running = running
	return nil
}

func (s *Server) terminalConversationView(ctx context.Context, id string) (mobileSession, error) {
	s.conversationMu.Lock()
	defer s.conversationMu.Unlock()
	m := &s.mobileCLI
	m.mu.Lock()
	record, err := m.record(s.profile, id)
	m.mu.Unlock()
	if err != nil {
		return mobileSession{}, err
	}
	var current terminalConversation
	input := map[string]any{"cli_id": record.CLIID, "config": record.GuestConfig, "connection": record.Connection, "account": record.Account.GuestID}
	if err := s.windowsConversationInput(record, input); err != nil {
		return mobileSession{}, err
	}
	if err := s.runCLIChannel(ctx, "view", input, &current); err != nil {
		return mobileSession{}, err
	}
	root := s.cliWorkspace(record.Workspace)
	if legacy := s.macOSLegacyWorkspace(record); legacy != "" {
		root = legacy
	}
	if s.windowsGuest != nil {
		root = record.GuestWorkspace
	}
	if current.Workspace == "" && !current.HasHistory && record.TerminalID != "" {
		if record.Switch != nil && record.Switch.Phase == "starting" && !current.Running {
			if owner := s.terminals.get(record.TerminalID); owner == nil || !owner.ownsConversation() {
				m.mu.Lock()
				record.Switch.Phase, record.Switch.Error = "prepared", "目标账号未建立会话，请重试交接"
				record.changed()
				err = writePrivate(mobileRecordPath(s.profile, id), record)
				m.mu.Unlock()
				if err != nil {
					return mobileSession{}, err
				}
			}
		}
		return record.snapshot(), nil
	}
	if !cliWorkspaceWithin(current.Workspace, root, s.windowsGuest != nil) {
		return mobileSession{}, errors.New("终端的项目已变化，请重新读取会话列表")
	}
	if terminal := s.terminals.get(record.TerminalID); terminal != nil && current.Connection != "" && current.Workspace != "" {
		terminal.mu.Lock()
		if terminal.connectionID == current.Connection {
			terminal.conversationEnded = !current.Running
		}
		terminal.mu.Unlock()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Reload after SSH so another request's identity and terminal attachment are
	// retained. The guest owns transcript content, the host owns the public ID.
	record, err = m.record(s.profile, id)
	if err != nil {
		return mobileSession{}, err
	}
	if record.Deleted {
		return mobileSession{}, os.ErrNotExist
	}
	before, _ := json.Marshal(record)
	record.Turns = adoptedMobileTurns(record.LegacyTurns, current.Turns)
	if record.Turns == nil {
		record.Turns = []mobileTurn{}
	}
	record.Title, record.ConnectionNotice = current.Title, current.ConnectionNotice
	if s.macOSLegacyWorkspace(record) != "" {
		record.ConnectionNotice = strings.TrimSpace(record.ConnectionNotice + "\n" + macOSLegacyWorkspaceNotice)
	}
	record.CanSend, record.HistoryTruncated = &current.CanSend, current.HistoryTruncated
	record.GuestConfig, record.Connection = current.Config, current.Connection
	record.GuestWorkspace = current.Workspace
	record.Resume = record.Resume || current.HasHistory
	record.TerminalRunning = current.Running
	record.Active = current.Active
	record.AccountFailure = current.AccountFailure
	if current.NativeHistory {
		record.ResumeTranscript = ""
	}
	if record.Switch != nil && record.Switch.Phase == "starting" && current.CLIID == record.Switch.TargetID {
		if current.ChannelReady && !current.ResumePending || current.HasHistory && !current.Running {
			record.Switch.Phase, record.Switch.Error = "completed", ""
			if current.NativeHistory {
				record.ResumeTranscript = ""
			}
		} else if !current.Running {
			if owner := s.terminals.get(record.TerminalID); owner == nil || !owner.ownsConversation() {
				record.Switch.Phase, record.Switch.Error = "prepared", "目标账号未建立会话，请重试交接"
			}
		}
	}
	after, _ := json.Marshal(record)
	if string(before) != string(after) {
		record.Revision++
		record.UpdatedAt = time.UnixMilli(int64(current.UpdatedAt * 1000)).UTC()
		if err := writePrivate(mobileRecordPath(s.profile, id), record); err != nil {
			return mobileSession{}, err
		}
		if m.catalog != nil {
			m.catalog[id] = record.summary()
		}
	}
	return record.snapshot(), nil
}

func (s *Server) terminalConversationAction(ctx context.Context, id string, input map[string]any) (mobileSession, error) {
	if !s.op.TryLock() {
		return mobileSession{}, errTerminalBusy
	}
	defer s.op.Unlock()
	s.account.mu.Lock()
	authorized := s.mobileAuthorized(ctx)
	s.account.mu.Unlock()
	if !authorized {
		return mobileSession{}, errMobileLocked
	}
	m := &s.mobileCLI
	m.mu.Lock()
	record, err := m.record(s.profile, id)
	m.mu.Unlock()
	if err != nil {
		return mobileSession{}, err
	}
	if record.Deleted {
		return mobileSession{}, os.ErrNotExist
	}
	if input["action"] == "send" {
		for _, previous := range record.LegacyTurns {
			if previous.ID == input["request_id"] {
				if previous.Prompt != input["text"] {
					return mobileSession{}, errors.New("同一发送编号已用于另一条消息")
				}
				view := record.snapshot()
				view.AcknowledgedRequestID = previous.ID
				return view, nil
			}
		}
	}
	if record.Connection == "" {
		return mobileSession{}, errors.New("会话连接尚未就绪，请稍后重试")
	}
	if record.Archived {
		return mobileSession{}, errors.New("此会话已归档，请先撤销归档")
	}
	input["cli_id"], input["connection"] = record.CLIID, record.Connection
	if err := s.windowsConversationInput(record, input); err != nil {
		return mobileSession{}, err
	}
	var response struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := s.runCLIChannel(ctx, "rpc", input, &response); err != nil {
		return mobileSession{}, err
	}
	if !response.OK {
		return mobileSession{}, errors.New(response.Error)
	}
	if input["action"] == "send" {
		// The guest already committed this message. Return its receipt now;
		// fetching the transcript is a separate read, not part of admission.
		s.account.mu.Lock()
		defer s.account.mu.Unlock()
		if !s.mobileAuthorized(ctx) {
			return mobileSession{}, errMobileLocked
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		record, err = m.record(s.profile, id)
		if err != nil {
			return mobileSession{}, err
		}
		view := record.snapshot()
		requestID, _ := input["request_id"].(string)
		if findMobileTurn(record, requestID) == nil {
			text, _ := input["text"].(string)
			view.Turns = append(view.Turns, mobileTurn{ID: requestID, Prompt: text, Status: "queued"})
		}
		view.AcknowledgedRequestID = requestID
		return view, nil
	}
	return s.terminalConversationView(ctx, id)
}

func (s *Server) prepareTerminalConversation(workspace string, selection claudeSelection, existing string) (string, *mobileRecord, error) {
	s.mu.Lock()
	launcher := claudeTerminalCommand(s.preferences)
	s.mu.Unlock()
	s.claudeAccounts.mu.Lock()
	list, loadErr := s.loadClaudeAccounts()
	s.claudeAccounts.mu.Unlock()
	if loadErr != nil {
		return "", nil, loadErr
	}
	m := &s.mobileCLI
	id := existing
	if id == "" {
		id = uuid.NewString()
	}
	if _, err := m.create(s.profile, workspace, id); err != nil {
		return "", nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record, err := m.record(s.profile, id)
	if err != nil {
		return "", nil, err
	}
	legacyWorkspace := ""
	if existing != "" {
		legacyWorkspace = s.macOSLegacyWorkspace(record)
	}
	if selection.ID == "" && selection.Subject == "" {
		selection.GuestID = ""
	}
	prefix, err := mobileLaunchArgs(launcher, selection)
	if err != nil {
		return "", nil, err
	}
	record.Terminal, record.Account, record.Launcher = true, selection, launcher
	if record.SwitchPolicy == "" {
		record.SwitchPolicy = list.Scheduling.SwitchPolicy
		if a := list.find(selection.ID); a != nil {
			record.AccountLabel = a.Label
		}
	}
	record.TerminalID, record.TerminalRunning = "", true
	record.Connection = uuid.NewString()
	if record.ResumeTranscript != "" && record.Switch != nil {
		record.Connection = record.Switch.ID
	}
	payload := map[string]any{"id": record.Connection, "session_id": record.CLIID, "prefix": prefix, "config": record.GuestConfig}
	if record.Resume {
		payload["resume"] = record.CLIID
	}
	if record.ResumeTranscript != "" {
		payload["resume_transcript"] = record.ResumeTranscript
		payload["resume"] = ""
		payload["switch_id"] = record.Switch.ID
		payload["continue_after_switch"] = record.Switch.Continue
	}
	if s.macosGuest != nil && record.GuestWorkspace != "" {
		root := s.cliWorkspace(workspace)
		if legacyWorkspace != "" {
			root = legacyWorkspace
		}
		if !cliWorkspaceWithin(record.GuestWorkspace, root, false) {
			return "", nil, errors.New("会话目录不在已授权的 macOS 项目内")
		}
		payload["project_subdir"] = strings.TrimPrefix(strings.TrimPrefix(record.GuestWorkspace, root), "/")
	}
	data, _ := json.Marshal(payload)
	record.CanSend = new(bool)
	record.ConnectionNotice = ""
	if legacyWorkspace != "" {
		record.ConnectionNotice = macOSLegacyWorkspaceNotice
	}
	if s.windowsGuest != nil {
		binding, err := s.windowsProjectForHost(workspace)
		if err != nil {
			return "", nil, err
		}
		record.GuestWorkspace = binding.Path
		if err := writePrivate(mobileRecordPath(s.profile, id), record); err != nil {
			return "", nil, err
		}
		return string(data), record, nil
	}
	body := s.cliHelperCommand("launch", string(data)) + "; exec bash -i"
	directory := record.GuestWorkspace
	if directory == "" {
		directory = s.cliWorkspace(workspace)
	}
	record.GuestWorkspace = directory
	if err := writePrivate(mobileRecordPath(s.profile, id), record); err != nil {
		return "", nil, err
	}
	if s.macosGuest != nil {
		if legacyWorkspace != "" {
			return "cd -- " + shellescape.Quote(legacyWorkspace) + " && exec " + s.cliHelperCommand("macos-launch", string(data)), record, nil
		}
		return s.macOSClaudeLaunch(workspace, string(data)), record, nil
	}
	script := "cd " + shellescape.Quote(directory) + " && exec env TERM=xterm-256color COLORTERM=truecolor CAGE_WORKBENCH=1 bash -lic " + shellescape.Quote(body)
	return script, record, nil
}

func (s *Server) isTerminalConversation(id string) bool {
	s.mobileCLI.mu.Lock()
	defer s.mobileCLI.mu.Unlock()
	record, err := s.mobileCLI.record(s.profile, id)
	return err == nil && record.Terminal
}

func (s *Server) stopTerminalConversation(ctx context.Context, view mobileSession, turnID string) (mobileSession, error) {
	if len(view.Turns) == 0 || view.Turns[len(view.Turns)-1].ID != turnID || view.Turns[len(view.Turns)-1].Status != "running" {
		return view, errors.New("本轮运行状态已变化，请重新读取")
	}
	s.mobileCLI.mu.Lock()
	record, err := s.mobileCLI.record(s.profile, view.ID)
	s.mobileCLI.mu.Unlock()
	if err != nil {
		return view, err
	}
	terminal := s.terminals.get(record.TerminalID)
	if terminal == nil {
		return view, errors.New("请在电脑终端停止这次运行")
	}
	s.account.mu.Lock()
	if !s.mobileAuthorized(ctx) {
		s.account.mu.Unlock()
		return view, errMobileLocked
	}
	err = terminal.enqueueInput(ctx, "\x1b")
	s.account.mu.Unlock()
	if err != nil {
		return view, err
	}
	return s.terminalConversationAction(ctx, view.ID, map[string]any{"action": "stop"})
}
