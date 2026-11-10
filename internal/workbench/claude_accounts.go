package workbench

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/mail"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// A profile ID selects a guest CLI configuration directory. Only public status
// fields enter this registry; the official CLI owns authentication in the VM.
type claudeAccount struct {
	ID             string       `json:"id"`
	Label          string       `json:"label"`
	Source         string       `json:"source"`
	ProfileID      string       `json:"profile_id,omitempty"`
	Enabled        bool         `json:"enabled"`
	Status         string       `json:"status"`
	Email          string       `json:"email,omitempty"`
	Organization   string       `json:"organization,omitempty"`
	Plan           string       `json:"plan,omitempty"`
	HandoffGroup   string       `json:"handoff_group,omitempty"`
	Quota          *claudeQuota `json:"quota,omitempty"`
	CheckedAt      int64        `json:"checked_at,omitempty"`
	UsageStatus    string       `json:"usage_status,omitempty"`
	UsageCheckedAt int64        `json:"usage_checked_at,omitempty"`
	UsageChecking  bool         `json:"usage_checking,omitempty"`
}

type claudeQuota struct {
	FiveHour   *claudeWindow `json:"five_hour,omitempty"`
	SevenDay   *claudeWindow `json:"seven_day,omitempty"`
	ObservedAt int64         `json:"observed_at"`
}

type claudeWindow struct {
	UsedPercentage float64 `json:"used_percentage"`
	ResetsAt       int64   `json:"resets_at"`
	ObservedAt     int64   `json:"observed_at,omitempty"`
}

type claudeGuestSnapshot struct {
	UsageStatus    string       `json:"usage_status,omitempty"`
	UsageCheckedAt int64        `json:"usage_checked_at,omitempty"`
	LoggedIn       bool         `json:"logged_in"`
	Email          string       `json:"email"`
	Organization   string       `json:"organization"`
	Plan           string       `json:"plan"`
	Quota          *claudeQuota `json:"quota"`
}

type claudeAccountList struct {
	Accounts     []claudeAccount  `json:"accounts"`
	PendingLogin string           `json:"pending_login,omitempty"`
	Scheduling   claudeScheduling `json:"scheduling"`
}

type claudeScheduling struct {
	Mode         string `json:"mode"`
	SwitchPolicy string `json:"switch_policy"`
}

func (p claudeScheduling) defaults() claudeScheduling {
	if p.Mode == "" {
		p.Mode = "balanced"
	}
	if p.SwitchPolicy == "" {
		p.SwitchPolicy = "ask"
	}
	return p
}

func (p claudeScheduling) valid() bool {
	return (p.Mode == "balanced" || p.Mode == "sequential") && (p.SwitchPolicy == "ask" || p.SwitchPolicy == "allow" || p.SwitchPolicy == "wait")
}

type claudeAccountManager struct {
	usageProbe     func(context.Context, string, string) (claudeGuestSnapshot, error)
	usageActive    map[string]*claudeUsageCall
	panelRunning   bool
	panelUntil     time.Time
	detectedUsage  map[string]*claudeAccount
	defaultGate    chan struct{}
	loginNotice    string
	defaultChecked time.Time
	defaultReading claudeGuestSnapshot
	defaultError   error
	probe          func(context.Context, string) (claudeGuestSnapshot, error)
	mu             sync.Mutex
	lastPick       string
	recent         map[string]claudeProjectPick
	refreshing     map[string]*claudeRefresh
	attempted      map[string]time.Time
}

type claudeRefresh struct {
	done chan struct{}
	err  error
}

const claudeQuotaFresh = 15 * time.Minute

func validClaudeID(id string) bool {
	_, err := hex.DecodeString(id)
	return len(id) == 24 && err == nil && strings.ToLower(id) == id
}

func (s *Server) loadClaudeAccounts() (claudeAccountList, error) {
	list := claudeAccountList{Accounts: []claudeAccount{}, Scheduling: (claudeScheduling{}).defaults()}
	data, err := os.ReadFile(filepath.Join(s.profile, "claude-accounts.json"))
	if os.IsNotExist(err) {
		return list, nil
	}
	if err != nil || json.Unmarshal(data, &list) != nil || list.Accounts == nil {
		return list, errors.New("Claude 账号文件无法读取，请检查配置目录或恢复备份")
	}
	list.Scheduling = list.Scheduling.defaults()
	if !list.Scheduling.valid() {
		return list, errors.New("Claude 调度设置无效，请检查配置文件")
	}
	ids := make(map[string]bool)
	profiles := make(map[string]bool)
	for i := range list.Accounts {
		a := &list.Accounts[i]
		if a.Source == "" {
			a.Source = "managed"
		}
		if !validClaudeID(a.ID) || ids[a.ID] || a.Source != "managed" && a.Source != "default" || a.Source == "default" && (a.Email == "" || a.ProfileID != "") || a.ProfileID != "" && !validClaudeID(a.ProfileID) || len(a.HandoffGroup) > 64 {
			return list, errors.New("Claude 账号文件含无效或重复的记录，请从备份恢复")
		}
		ids[a.ID] = true
		if a.Source == "managed" {
			if profiles[a.guestID()] {
				return list, errors.New("多个账号引用了同一个独立登录目录，请从备份恢复")
			}
			profiles[a.guestID()] = true
		}
	}
	if list.PendingLogin != "" && (!validClaudeID(list.PendingLogin) || ids[list.PendingLogin] || profiles[list.PendingLogin]) {
		return list, errors.New("Claude 登录记录损坏，请恢复备份")
	}
	return list, nil
}

func (s *Server) saveClaudeAccounts(list claudeAccountList) error {
	if err := writePrivate(filepath.Join(s.profile, "claude-accounts.json"), list); err != nil {
		return errors.New("Claude 账号设置未保存，请检查配置目录权限和磁盘空间")
	}
	return nil
}

func (a claudeAccount) guestID() string {
	if a.Source == "default" {
		return "default"
	}
	if a.ProfileID != "" {
		return a.ProfileID
	}
	return a.ID
}

func (list claudeAccountList) find(id string) *claudeAccount {
	for i := range list.Accounts {
		if list.Accounts[i].ID == id {
			return &list.Accounts[i]
		}
	}
	return nil
}

func validClaudeLabel(label string) bool {
	return label != "" && utf8.RuneCountInString(label) <= 64 && strings.IndexFunc(label, unicode.IsControl) < 0
}

func (list *claudeAccountList) change(input claudeAccountInput) error {
	label := strings.TrimSpace(input.Label)
	if input.Action != "update" {
		return errors.New("未知 Claude 账号操作")
	}
	a := list.find(input.ID)
	if a == nil {
		return errors.New("未找到 Claude 账号，请刷新面板")
	}
	if input.Label != "" {
		if !validClaudeLabel(label) && (label == "" || label != a.Email) {
			return errors.New("备注最多 64 个字符，不能含控制字符")
		}
		a.Label = label
	}
	if input.Enabled != nil {
		a.Enabled = *input.Enabled
	}
	if input.HandoffGroup != nil {
		group := strings.TrimSpace(*input.HandoffGroup)
		if len(group) > 64 || strings.IndexFunc(group, unicode.IsControl) >= 0 {
			return errors.New("接续分组最多 64 字节，不能含控制字符")
		}
		a.HandoffGroup = group
	}
	return nil
}

func (r claudeGuestSnapshot) email() string {
	email := strings.ToLower(strings.TrimSpace(r.Email))
	mailbox, err := mail.ParseAddress(email)
	if !r.LoggedIn || err != nil || mailbox.Address != email {
		return ""
	}
	return email
}

func (r claudeGuestSnapshot) subject() string {
	if email := r.email(); email != "" {
		return claudeIdentityHash(email, r.Organization)
	}
	return ""
}

func validClaudeWindow(w *claudeWindow) bool {
	return w != nil && !math.IsNaN(w.UsedPercentage) && w.UsedPercentage >= 0 && w.UsedPercentage <= 100 && w.ResetsAt >= 0
}

// A missing reading cannot invalidate an existing reading, or make an old
// reading fresh. Never attach a previous identity's quota to a new identity.
func (list claudeAccountList) observe(id string, observed claudeGuestSnapshot, now time.Time) error {
	a := list.find(id)
	if a == nil {
		return errors.New("未找到 Claude 账号")
	}
	a.CheckedAt = now.Unix()
	email := observed.email()
	if email == "" {
		a.Status = "signed_out"
		return nil
	}
	organization := strings.TrimSpace(observed.Organization)
	if a.Email != "" && (!strings.EqualFold(a.Email, email) || a.Organization != organization) {
		a.Status = "changed"
		return nil
	}
	for _, other := range list.Accounts {
		if other.ID != id && strings.EqualFold(other.Email, email) && other.Organization == organization {
			a.Status = "duplicate"
			return nil
		}
	}
	a.Status, a.Email, a.Organization, a.Plan = "ready", email, organization, observed.Plan
	a.mergeQuota(observed.Quota, now)
	return nil
}

func (a *claudeAccount) mergeQuota(q *claudeQuota, now time.Time) bool {
	if q == nil || q.ObservedAt <= 0 || q.ObservedAt > now.Unix() {
		return false
	}
	next := claudeQuota{}
	if a.Quota != nil {
		next = *a.Quota
	}
	merge := func(old, reading *claudeWindow) *claudeWindow {
		if !validClaudeWindow(reading) {
			return old
		}
		window := *reading
		if window.ObservedAt == 0 {
			window.ObservedAt = q.ObservedAt
		}
		if window.ObservedAt < 0 || window.ObservedAt > q.ObservedAt || old != nil && old.ObservedAt > window.ObservedAt {
			return old
		}
		if old != nil && window == *old {
			return old
		}
		return &window
	}
	next.FiveHour = merge(next.FiveHour, q.FiveHour)
	next.SevenDay = merge(next.SevenDay, q.SevenDay)
	if next.FiveHour == nil && next.SevenDay == nil || a.Quota != nil && next == *a.Quota {
		return false
	}
	next.ObservedAt = max(next.ObservedAt, q.ObservedAt)
	a.Quota = &next
	return true
}

func claudeIdentityHash(email, organization string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email)) + "\n" + strings.TrimSpace(organization)))
	return hex.EncodeToString(digest[:])
}

// A fully used window still blocks new sessions until its actual reset, even
// if the snapshot is stale. An expired or unreported window stays unknown.
func (a claudeAccount) headroom(now time.Time) (float64, bool) {
	if a.Quota == nil {
		return -1, true
	}
	least := -1.0
	for _, w := range []*claudeWindow{a.Quota.FiveHour, a.Quota.SevenDay} {
		if !validClaudeWindow(w) || w.ResetsAt > 0 && w.ResetsAt <= now.Unix() {
			continue
		}
		fresh := now.Sub(time.Unix(w.ObservedAt, 0)) <= claudeQuotaFresh
		if w.UsedPercentage >= 100 && (w.ResetsAt > now.Unix() || fresh) {
			return 0, false
		}
		if fresh {
			remaining := 100 - w.UsedPercentage
			if least < 0 || remaining < least {
				least = remaining
			}
		}
	}
	return least, true
}

// A selection carries the identity verified for this launch. The caller
// must not replace Subject or GuestID by looking the record up again.
type claudeSelection struct{ ID, GuestID, Subject string }

func (s *Server) selectClaudeAccount(requestedID string, login bool, workspace string) (claudeSelection, error) {
	var empty claudeSelection
	if !login && requestedID != "" && requestedID != "auto" {
		return empty, errors.New("Claude 终端会自动分配账号")
	}
	m := &s.claudeAccounts
	m.mu.Lock()
	list, err := s.loadClaudeAccounts()
	if err == nil && list.PendingLogin != "" && requestedID == "" && !s.claudeLoginActive(list.PendingLogin) {
		m.mu.Unlock()
		// Restore a successful login from before a controller restart or lock
		// before a new login could write into the same guest directory.
		if epoch, ok := s.account.access(); ok {
			if probeErr := s.recoverClaudePending(epoch, list.PendingLogin, login); login && probeErr != nil {
				return empty, probeErr
			}
		}
		m.mu.Lock()
		list, err = s.loadClaudeAccounts()
	}
	if err == nil && login {
		if requestedID == "" {
			if list.PendingLogin == "" {
				list.PendingLogin = randomToken()[:24]
				m.loginNotice = ""
				err = s.saveClaudeAccounts(list)
			}
			requestedID = list.PendingLogin
		} else if a := list.find(requestedID); a == nil || a.Source != "managed" {
			err = errors.New("请在普通终端管理环境的默认登录")
		}
	}
	m.mu.Unlock()
	if err != nil {
		return empty, err
	}
	if login {
		guestID := requestedID
		if a := list.find(requestedID); a != nil {
			guestID = a.guestID()
		}
		return claudeSelection{ID: requestedID, GuestID: guestID}, nil
	}
	if len(list.Accounts) == 0 {
		return empty, nil
	}
	return s.allocateClaudeAccount(list, workspace, s.readClaudeGuestQuotas)
}

type claudeGuestQuota struct {
	claudeQuota
	Identity string `json:"identity"`
}

func (s *Server) readClaudeGuestQuotas(ctx context.Context, ids []string) (map[string]claudeGuestQuota, error) {
	for _, id := range ids {
		if !validClaudeID(id) {
			return nil, errors.New("Claude 账号 ID 无效")
		}
	}
	// s.guest bounds its output to 64 KiB. Even a large pool uses one
	// server-controlled deadline; keep completed chunks when a later one fails.
	readings := make(map[string]claudeGuestQuota)
	for chunk := range slices.Chunk(ids, 96) {
		if s.windowsGuest != nil {
			if err := s.windowsCLIRequest(ctx, map[string]any{"action": "account", "mode": "quotas", "ids": chunk}, &readings); err != nil {
				return readings, err
			}
			continue
		}
		data, err := s.guest(ctx, "exec /usr/local/bin/cage-claude-account quotas "+strings.Join(chunk, " "))
		if err != nil {
			return readings, err
		}
		if err := json.Unmarshal(data, &readings); err != nil {
			return readings, err
		}
	}
	return readings, nil
}

// The status line may have written a newer reading while the account panel
// was closed. Fetch only its small local files in one SSH command. The guest
// does not start the CLI, read tokens, or contact Anthropic for this read.
func (s *Server) allocateClaudeAccount(previous claudeAccountList, workspace string, read func(context.Context, []string) (map[string]claudeGuestQuota, error)) (claudeSelection, error) {
	var empty claudeSelection
	epoch, unlocked := s.account.access()
	if !unlocked {
		return empty, errors.New("Cage 已锁定，请先解锁")
	}
	var ids []string
	checkDefault := false
	for _, a := range previous.Accounts {
		if a.Source == "default" {
			checkDefault = checkDefault || a.Enabled
		} else if a.Enabled && a.Status == "ready" {
			ids = append(ids, a.guestID())
		}
	}
	var readings map[string]claudeGuestQuota
	if len(ids) > 0 {
		ctx, cancel := context.WithTimeout(epoch, 4*time.Second)
		readings, _ = read(ctx, ids) // Retain the last snapshot when SSH is unavailable.
		cancel()
	}
	defaultSubject := ""
	if checkDefault {
		if reading, _, err := s.inspectClaudeDefault(epoch, true); err == nil {
			defaultSubject = reading.subject()
		}
	}
	m := &s.claudeAccounts
	m.mu.Lock()
	defer m.mu.Unlock()
	if epoch.Err() != nil {
		return empty, errors.New("Cage 已锁定，请先解锁")
	}
	list, err := s.loadClaudeAccounts()
	if err != nil {
		return empty, err
	}
	now := time.Now()
	updated := false
	for i := range list.Accounts {
		a := &list.Accounts[i]
		q, ok := readings[a.guestID()]
		if a.Enabled && a.Status == "ready" && ok && q.Identity == claudeIdentityHash(a.Email, a.Organization) {
			if a.mergeQuota(&q.claudeQuota, now) {
				updated = true
			}
		}
	}
	if updated {
		if err = s.saveClaudeAccounts(list); err != nil {
			return empty, err
		}
	}
	active := s.mobileCLI.activeAccounts()
	logins := make(map[string]bool)
	for _, session := range s.terminals.snapshot() {
		if session["closed"] == true {
			continue
		}
		id, _ := session["claude_account_id"].(string)
		if session["kind"] == "claude-login" {
			logins[id] = true
		} else {
			active[id]++
		}
	}
	recent := m.recent[workspace]
	preferred := ""
	if now.Sub(recent.at) < claudeProjectAffinity {
		preferred = recent.id
	}
	// Only a positively verified matching default profile can be admitted.
	for i := range list.Accounts {
		a := &list.Accounts[i]
		if a.Source == "default" && (defaultSubject == "" || defaultSubject != claudeIdentityHash(a.Email, a.Organization)) {
			a.Enabled = false // local selection view, not the persisted preference
		}
	}
	selected := pickClaudeAccount(list, active, logins, m.lastPick, preferred, now)
	if selected == "" {
		return empty, errors.New("暂无可分配的 Claude 账号。请在账号面板检查登录、参与状态和额度重置时间")
	}
	a := list.find(selected)
	return claudeSelection{ID: selected, GuestID: a.guestID(), Subject: claudeIdentityHash(a.Email, a.Organization)}, nil
}

// Start at the account following the last allocation. Among eligible accounts,
// prefer fewer live sessions, then the largest known remaining minimum window.
func pickClaudeAccount(list claudeAccountList, active map[string]int, logins map[string]bool, last, preferred string, now time.Time) string {
	if list.Scheduling.Mode == "sequential" {
		for _, a := range list.Accounts {
			if _, eligible := a.headroom(now); a.Enabled && a.Status == "ready" && !logins[a.ID] && eligible {
				return a.ID
			}
		}
		return ""
	}
	start := slices.IndexFunc(list.Accounts, func(a claudeAccount) bool { return a.ID == last }) + 1
	best := ""
	var bestActive int
	var bestRoom float64
	for offset := range list.Accounts {
		a := list.Accounts[(start+offset)%len(list.Accounts)]
		if !a.Enabled || a.Status != "ready" || logins[a.ID] {
			continue
		}
		room, eligible := a.headroom(now)
		if eligible && (best == "" || active[a.ID] < bestActive || active[a.ID] == bestActive && room > bestRoom) {
			best, bestActive, bestRoom = a.ID, active[a.ID], room
		}
	}
	// Keep a recent project's profile only when it has the same load and
	// comparable headroom (both unknown, or within five percentage points).
	if a := list.find(preferred); a != nil && a.Enabled && a.Status == "ready" && !logins[a.ID] && active[a.ID] == bestActive {
		room, ok := a.headroom(now)
		if ok && (room == bestRoom || room >= 0 && bestRoom-room <= min(5, bestRoom*0.1)) {
			return a.ID
		}
	}
	return best
}

type claudeProjectPick struct {
	id string
	at time.Time
}

const claudeProjectAffinity = 5 * time.Minute

func (s *Server) noteClaudeSelection(id, workspace string) {
	m := &s.claudeAccounts
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastPick = id
	if workspace == "" {
		return
	}
	if m.recent == nil {
		m.recent = make(map[string]claudeProjectPick)
	}
	now := time.Now()
	var oldest string
	for path, pick := range m.recent {
		if now.Sub(pick.at) >= claudeProjectAffinity {
			delete(m.recent, path)
		} else if oldest == "" || pick.at.Before(m.recent[oldest].at) {
			oldest = path
		}
	}
	m.recent[workspace] = claudeProjectPick{id: id, at: now}
	if len(m.recent) > 64 {
		delete(m.recent, oldest)
	}
}
