package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"al.essio.dev/pkg/shellescape"
)

// Older phones created headless sessions. Reuse their CLI UUID only after the
// original provider binding has been checked, without sending a model request.
func (s *Server) promoteMobileConversation(ctx context.Context, id string) error {
	if !s.op.TryLock() {
		return errTerminalBusy
	}
	defer s.op.Unlock()
	m := &s.mobileCLI
	m.mu.Lock()
	record, err := m.record(s.profile, id)
	active := m.active[id] != nil
	m.mu.Unlock()
	if err != nil {
		return err
	}
	if record.Terminal {
		return nil
	}
	if active || record.turnActive() {
		return errors.New("会话仍在运行，请等待本轮完成后打开")
	}
	selection, launcher, _, err := s.prepareMobileLaunch(record)
	if err != nil {
		return err
	}
	if record.SingleTurn {
		return errors.New("此会话的原账号与上游无法验证，请核对原配置后新建会话")
	}
	config := ""
	if record.Resume {
		prefix, err := mobileLaunchArgs(launcher, selection)
		if err != nil {
			return err
		}
		args := mobileIdentityCommand(prefix, []string{"--cage-verify", "--setting-sources", "user", "--resume", record.CLIID})
		// Match the login environment and project of the original mobile turn.
		body := "exec 1>&3 3>&-; cd -- " + shellescape.Quote(guestPath(record.Workspace)) + " && exec " + shellescape.QuoteCommand(args)
		command := "exec /bin/bash -c " + shellescape.Quote("exec 3>&1 1>&2; exec /bin/bash -lc "+shellescape.Quote(body))
		check, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		cmd := s.sshCommand(check, "-T", s.sshTarget(), "--", command)
		cmd.Stderr = io.Discard
		cmd.WaitDelay = 2 * time.Second
		out, err := cmd.Output()
		var result struct {
			Config string `json:"config"`
		}
		if err != nil || len(out) > 4096 || json.Unmarshal(out, &result) != nil || !strings.HasPrefix(result.Config, "/") {
			return errors.New("无法验证本会话原先使用的账号与上游，请检查原配置后重试")
		}
		config = result.Config
	}
	s.account.mu.Lock()
	defer s.account.mu.Unlock()
	if !s.mobileAuthorized(ctx) {
		return errMobileLocked
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Metadata operations can run while the guest verifies the identity.
	record, err = m.record(s.profile, id)
	if err != nil {
		return err
	}
	if record.Archived || record.Deleted {
		return errors.New("此会话已归档或删除，请先恢复会话")
	}
	record.LegacyTurns = append([]mobileTurn(nil), record.Turns...)
	record.Terminal, record.Account, record.Launcher = true, selection, launcher
	record.GuestConfig, record.GuestWorkspace = config, guestPath(record.Workspace)
	record.changed()
	return writePrivate(mobileRecordPath(s.profile, id), record)
}

// Preserve preflight failures that never reached the CLI transcript. Completed
// turns come from the original JSONL, with their mobile request IDs retained.
func adoptedMobileTurns(legacy, current []mobileTurn) []mobileTurn {
	if len(legacy) == 0 {
		return current
	}
	result := make([]mobileTurn, 0, len(legacy)+len(current))
	position := 0
	for _, old := range legacy {
		if position < len(current) && old.Prompt == current[position].Prompt {
			turn := current[position]
			turn.ID = old.ID
			result = append(result, turn)
			position++
		} else {
			result = append(result, old)
		}
	}
	return append(result, current[position:]...)
}
