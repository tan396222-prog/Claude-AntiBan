package workbench

import (
	"context"
	"errors"
	"time"
)

const (
	claudePanelLease    = 30 * time.Second
	claudeUsageInterval = time.Minute
)

var errClaudeUsageBusy = errors.New("正在检查其他账号的额度，请稍后重试")

func claudeUsageDue(checked int64) bool {
	return checked == 0 || time.Since(time.Unix(checked, 0)) >= claudeUsageInterval
}

type claudeUsageCall struct {
	done chan struct{}
	err  error
}

type claudeUsageTarget struct {
	id, guest, subject string
}

// Only opening a visible account panel starts a batch. It uses one worker,
// leaving one slot available for an explicit refresh. Reading panel snapshots
// can keep an existing batch alive but never starts another one.
func (s *Server) startClaudePanelUsage(list claudeAccountList, detected *claudeDetected) {
	if s.usageAvailability() != nil {
		return
	}
	epoch, unlocked := s.account.access()
	if !unlocked {
		return
	}
	m := &s.claudeAccounts
	m.mu.Lock()
	m.panelUntil = time.Now().Add(claudePanelLease)
	if m.panelRunning {
		m.mu.Unlock()
		return
	}
	var targets []claudeUsageTarget
	if detected != nil && claudeUsageDue(detected.UsageCheckedAt) {
		targets = append(targets, claudeUsageTarget{guest: "default", subject: detected.Identity})
	}
	for _, a := range list.Accounts {
		if a.Status != "ready" || a.Email == "" || !claudeUsageDue(a.UsageCheckedAt) {
			continue
		}
		subject := claudeIdentityHash(a.Email, a.Organization)
		if a.Source == "default" && (m.defaultError != nil || m.defaultReading.subject() != subject) {
			continue
		}
		targets = append(targets, claudeUsageTarget{a.ID, a.guestID(), subject})
	}
	if len(targets) == 0 {
		m.mu.Unlock()
		return
	}
	m.panelRunning = true
	m.mu.Unlock()
	ctx, finish, err := s.beginActivity(epoch)
	if err != nil {
		m.mu.Lock()
		m.panelRunning = false
		m.mu.Unlock()
		return
	}
	go func() {
		defer finish()
		defer func() { m.mu.Lock(); m.panelRunning = false; m.mu.Unlock() }()
		for i := 0; i < len(targets); {
			m.mu.Lock()
			visible := time.Now().Before(m.panelUntil)
			m.mu.Unlock()
			if !visible || ctx.Err() != nil || s.usageAvailability() != nil {
				return
			}
			if err := s.checkClaudeUsage(ctx, targets[i], true); !errors.Is(err, errClaudeUsageBusy) {
				i++
				continue
			}
			m.mu.Lock()
			var done <-chan struct{}
			for _, call := range m.usageActive {
				done = call.done
				break
			}
			m.mu.Unlock()
			if done != nil {
				select {
				case <-ctx.Done():
					return
				case <-done:
				}
			}
		}
	}()
}

func (s *Server) usageAvailability() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.guestUpdateRequired:
		return errors.New("请先同步环境组件后再查询额度")
	case s.closing || !s.ready:
		return errors.New("请先启动 Cage 环境后再查询额度")
	case s.networkPaused:
		return errors.New("网络已暂停，恢复连接后可查询订阅额度")
	default:
		return nil
	}
}

func (s *Server) refreshClaudeUsage(ctx context.Context, input claudeAccountInput) error {
	if err := s.usageAvailability(); err != nil {
		return err
	}
	if input.Action == "refresh_default" {
		current, _, err := s.inspectClaudeDefault(ctx, true)
		if err != nil {
			return err
		}
		if input.Identity == "" || current.subject() != input.Identity {
			return errors.New("默认登录身份已经变化，请刷新账号面板")
		}
		return s.checkClaudeUsage(ctx, claudeUsageTarget{guest: "default", subject: input.Identity}, false)
	}
	m := &s.claudeAccounts
	m.mu.Lock()
	list, err := s.loadClaudeAccounts()
	m.mu.Unlock()
	if err != nil {
		return err
	}
	a := list.find(input.ID)
	if a == nil {
		return errors.New("未找到要检查的账号")
	}
	if a.Email == "" {
		if err = s.refreshClaudeAccount(ctx, input.ID); err != nil {
			return err
		}
		m.mu.Lock()
		list, err = s.loadClaudeAccounts()
		m.mu.Unlock()
		if err != nil {
			return err
		}
		a = list.find(input.ID)
		if a == nil || a.Email == "" {
			return errors.New("尚未检测到此账号的有效订阅登录")
		}
	}
	return s.checkClaudeUsage(ctx, claudeUsageTarget{a.ID, a.guestID(), claudeIdentityHash(a.Email, a.Organization)}, false)
}

func (s *Server) checkClaudeUsage(parent context.Context, target claudeUsageTarget, automatic bool) error {
	if err := s.usageAvailability(); err != nil {
		return err
	}
	epoch, unlocked := s.account.access()
	if !unlocked {
		return errClaudeAccessLost
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	stop := context.AfterFunc(epoch, cancel)
	defer func() { stop(); cancel() }()
	m := &s.claudeAccounts
	m.mu.Lock()
	if call := m.usageActive[target.subject]; call != nil {
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-call.done:
			return call.err
		}
	}
	list, err := s.loadClaudeAccounts()
	if err != nil {
		m.mu.Unlock()
		return err
	}
	owner := s.usageOwner(list, target)
	if owner == nil {
		m.mu.Unlock()
		return errors.New("账号来源或身份已变化，请刷新面板")
	}
	if automatic && !claudeUsageDue(owner.UsageCheckedAt) {
		m.mu.Unlock()
		return nil
	}
	if len(m.usageActive) >= 2 {
		m.mu.Unlock()
		return errClaudeUsageBusy
	}
	if m.usageActive == nil {
		m.usageActive = make(map[string]*claudeUsageCall)
	}
	call := &claudeUsageCall{done: make(chan struct{})}
	m.usageActive[target.subject] = call
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.usageActive, target.subject)
		call.err = err
		close(call.done)
		m.mu.Unlock()
	}()
	probe := m.usageProbe
	if probe == nil {
		probe = s.readClaudeGuestUsage
	}
	reading, probeErr := probe(ctx, target.guest, target.subject)
	if epoch.Err() != nil {
		err = errClaudeAccessLost
		return err
	}
	if parent.Err() != nil {
		err = parent.Err()
		return err
	}
	if err = s.usageAvailability(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if epoch.Err() != nil {
		err = errClaudeAccessLost
		return err
	}
	if parent.Err() != nil {
		err = parent.Err()
		return err
	}
	list, err = s.loadClaudeAccounts()
	if err != nil {
		return err
	}
	owner = s.usageOwner(list, target)
	if owner == nil {
		err = errors.New("账号来源或身份已变化，未记录此次额度")
		return err
	}
	now := time.Now()
	status := reading.UsageStatus
	switch {
	case probeErr != nil || status == "error":
		status = "error"
	case status == "identity_changed" || reading.subject() != target.subject:
		status = "identity_changed"
		owner.Status = "changed"
		if target.guest == "default" {
			m.defaultChecked = time.Time{}
		}
	case status == "reported" || status == "partial" || status == "cached":
		owner.mergeQuota(reading.Quota, now)
	case status == "unavailable" || status == "not_applicable":
	default:
		status = "error"
	}
	if status != "error" && status != "identity_changed" {
		owner.Status = "ready"
		if reading.Plan != "" {
			owner.Plan = reading.Plan
		}
	}
	owner.UsageStatus, owner.UsageCheckedAt = status, now.Unix()
	if target.id != "" || list.bySubject(target.subject) == owner {
		err = s.saveClaudeAccounts(list)
	}
	return err
}

// Under the manager lock, resolve only the original guest source and identity.
// A discovered default identity has no disk record until the user joins it.
func (s *Server) usageOwner(list claudeAccountList, target claudeUsageTarget) *claudeAccount {
	if a := list.bySubject(target.subject); a != nil {
		if a.guestID() == target.guest && (target.id == "" || target.id == a.ID) {
			return a
		}
		return nil
	}
	m := &s.claudeAccounts
	if target.id != "" || target.guest != "default" || m.defaultReading.subject() != target.subject {
		return nil
	}
	return m.detectedUsage[target.subject]
}
