package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClaudeAccountPanelConcurrentWindowsAndLock(t *testing.T) {
	s := testServer(t)
	accountID := strings.Repeat("a", 24)
	list := claudeAccountList{Accounts: []claudeAccount{{ID: accountID, Label: "原名称", Enabled: true, Status: "ready"}}}
	if err := s.saveClaudeAccounts(list); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, mutation := range []string{`"label":"工作"`, `"enabled":false`} {
		wg.Add(1)
		go func(mutation string) {
			defer wg.Done()
			w := request(s, "POST", "/api/claude-accounts", s.origin, `{"action":"update","id":"`+accountID+`",`+mutation+`}`, true)
			if w.Code != http.StatusOK {
				t.Errorf("update: %d %s", w.Code, w.Body)
			}
		}(mutation)
	}
	wg.Wait()
	list, err := s.loadClaudeAccounts()
	if err != nil || len(list.Accounts) != 1 || list.Accounts[0].Enabled || list.Accounts[0].Label != "工作" {
		t.Fatalf("lost concurrent edit: %+v %v", list, err)
	}
	a := list.Accounts[0]
	w := request(s, "POST", "/api/claude-accounts", s.origin, `{"action":"update","id":"`+a.ID+`","label":"已归档","enabled":false}`, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	reopened := &Server{profile: s.profile}
	list, err = reopened.loadClaudeAccounts()
	if err != nil || list.Accounts[0].Enabled || list.Accounts[0].Label != "已归档" {
		t.Fatalf("updated settings did not persist: %+v %v", list, err)
	}
	w = request(s, "POST", "/api/claude-accounts", s.origin, `{"action":"update","id":"`+a.ID+`","enabled":true}`, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Fatal(w.Code, w.Body)
	}
	s.account.lock()
	if w = request(s, "GET", "/api/claude-accounts", "", "", true); w.Code != http.StatusLocked {
		t.Fatal("locked Cage exposed account data", w.Code)
	}
}

func TestClaudeAccountOnlyEmptyRegistryFallsBack(t *testing.T) {
	s := testServer(t)
	if id, err := s.selectClaudeAccount("", false, ""); err != nil || id.ID != "" {
		t.Fatalf("existing user compatibility: %q %v", id, err)
	}
	list := claudeAccountList{Accounts: []claudeAccount{{ID: strings.Repeat("a", 24), Label: "A", Enabled: true, Status: "pending"}}}
	if err := s.saveClaudeAccounts(list); err != nil {
		t.Fatal(err)
	}
	if _, err := s.selectClaudeAccount("", false, ""); err == nil {
		t.Fatal("unsigned managed account silently switched to legacy")
	}
	list.Accounts[0].Enabled = false
	list.Accounts[0].Status = "ready"
	if err := s.saveClaudeAccounts(list); err != nil {
		t.Fatal(err)
	}
	if _, err := s.selectClaudeAccount("", false, ""); err == nil {
		t.Fatal("disabled pool silently switched to legacy")
	}
	if id, err := s.selectClaudeAccount(list.Accounts[0].ID, true, ""); err != nil || id.ID != list.Accounts[0].ID {
		t.Fatalf("cannot reauthenticate paused profile: %q %v", id, err)
	}
	if err := os.WriteFile(filepath.Join(s.profile, "claude-accounts.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.selectClaudeAccount("", false, ""); err == nil {
		t.Fatal("corrupt registry silently switched to legacy")
	}
}

func TestClaudeGuestIdentityAndPartialQuota(t *testing.T) {
	now := time.Unix(1700000000, 0)
	list := claudeAccountList{Accounts: []claudeAccount{{ID: "one"}, {ID: "two"}}}
	first := claudeGuestSnapshot{LoggedIn: true, Email: "Work@example.org", Organization: "work", Quota: &claudeQuota{
		FiveHour: &claudeWindow{UsedPercentage: 80, ResetsAt: now.Add(time.Hour).Unix()}, SevenDay: &claudeWindow{UsedPercentage: 100, ResetsAt: now.Add(48 * time.Hour).Unix()}, ObservedAt: now.Unix(),
	}}
	_ = list.observe("one", first, now)
	_ = list.observe("two", first, now)
	if list.Accounts[0].Status != "ready" || list.Accounts[1].Status != "duplicate" || list.Accounts[1].Quota != nil {
		t.Fatalf("same signed-in subscription counted twice: %+v", list)
	}
	first.Quota = &claudeQuota{FiveHour: &claudeWindow{UsedPercentage: 32, ResetsAt: now.Add(3 * time.Hour).Unix()}, ObservedAt: now.Add(time.Hour).Unix()}
	_ = list.observe("one", first, now.Add(time.Hour))
	// A batched response can also carry an older weekly window together
	// with the new short-window timestamp.
	first.Quota.SevenDay = &claudeWindow{UsedPercentage: 100, ResetsAt: now.Add(48 * time.Hour).Unix(), ObservedAt: now.Unix()}
	_ = list.observe("one", first, now.Add(time.Hour))
	if list.Accounts[0].Quota.SevenDay.ObservedAt != now.Unix() {
		t.Fatal("missing weekly window updated its timestamp")
	}
	if _, eligible := list.Accounts[0].headroom(now.Add(time.Hour)); eligible {
		t.Fatal("stale exhausted weekly window became available")
	}
	if score, eligible := list.Accounts[0].headroom(now.Add(49 * time.Hour)); !eligible || score != -1 {
		t.Fatal("reset became a fabricated live reading", score, eligible)
	}
	first.Email = "new@example.org"
	_ = list.observe("one", first, now.Add(time.Hour))
	if list.Accounts[0].Status != "changed" || list.Accounts[0].Quota == nil || list.Accounts[0].Email != "work@example.org" {
		t.Fatal("silent replacement of a bound identity", list.Accounts[0])
	}
}

func TestClaudeAllocationReadsGuestWithoutOpeningPanel(t *testing.T) {
	s := testServer(t)
	list := claudeAccountList{Accounts: []claudeAccount{
		{ID: strings.Repeat("a", 24), Label: "A", Enabled: true, Status: "ready", Email: "a@example.org"},
		{ID: strings.Repeat("b", 24), Label: "B", Enabled: true, Status: "ready", Email: "b@example.org"},
		{ID: strings.Repeat("c", 24), Label: "C", Enabled: false, Status: "ready"},
	}}
	if err := s.saveClaudeAccounts(list); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	var calls int
	read := func(ctx context.Context, ids []string) (map[string]claudeGuestQuota, error) {
		calls++
		if !sort.StringsAreSorted(ids) || len(ids) != 2 {
			t.Fatalf("expected one batch of participating profiles: %v", ids)
		}
		q := claudeQuota{FiveHour: &claudeWindow{UsedPercentage: 100, ResetsAt: now.Add(time.Hour).Unix()}, ObservedAt: now.Unix()}
		return map[string]claudeGuestQuota{
			ids[0]: {claudeQuota: q, Identity: claudeIdentityHash("a@example.org", "")},
			ids[1]: {claudeQuota: q, Identity: claudeIdentityHash("someoneelse@example.org", "")},
		}, nil
	}
	want := list.Accounts[1].ID
	id, err := s.allocateClaudeAccount(list, "", read)
	if err != nil || id.ID != want || calls != 1 {
		t.Fatalf("did not react to A's new quota or accepted another identity's reading: %q %v", id, err)
	}
	id, err = s.allocateClaudeAccount(list, "", func(context.Context, []string) (map[string]claudeGuestQuota, error) {
		return nil, errors.New("VM temporarily unavailable")
	})
	if err != nil || id.ID != want {
		t.Fatalf("guest failure erased confirmed exhaustion: %q %v", id, err)
	}
	saved, _ := s.loadClaudeAccounts()
	if saved.Accounts[1].Quota != nil || saved.Accounts[0].Quota.FiveHour.UsedPercentage != 100 {
		t.Fatal("new observations did not persist with their source identity")
	}
}

func TestClaudeAccountRotationReservesActiveSessions(t *testing.T) {
	list := claudeAccountList{Accounts: []claudeAccount{{ID: "one", Enabled: true, Status: "ready"}, {ID: "two", Enabled: true, Status: "ready"}}}
	now := time.Now()
	if id := pickClaudeAccount(list, map[string]int{"one": 1}, nil, "two", "", now); id != "two" {
		t.Fatal("did not account for occupied terminal", id)
	}
	if id := pickClaudeAccount(list, nil, nil, "one", "", now); id != "two" {
		t.Fatal("equal profiles did not rotate", id)
	}
	if id := pickClaudeAccount(list, nil, map[string]bool{"two": true}, "one", "", now); id != "one" {
		t.Fatal("assigned profile currently signing in", id)
	}
}

func TestClaudeQuotaWireFormat(t *testing.T) {
	var q claudeGuestQuota
	if err := json.Unmarshal([]byte(`{"identity":"digest","five_hour":{"used_percentage":39,"resets_at":150},"observed_at":100}`), &q); err != nil || q.FiveHour == nil || q.FiveHour.UsedPercentage != 39 || q.Identity != "digest" {
		t.Fatalf("guest/host envelope mismatch: %+v %v", q, err)
	}
}

func TestClaudeProjectAffinityYieldsToAvailabilityAndLoad(t *testing.T) {
	s := testServer(t)
	list := claudeAccountList{Accounts: []claudeAccount{
		{ID: strings.Repeat("a", 24), Enabled: true, Status: "ready"},
		{ID: strings.Repeat("b", 24), Enabled: true, Status: "ready"},
	}}
	if err := s.saveClaudeAccounts(list); err != nil {
		t.Fatal(err)
	}
	noReadings := func(context.Context, []string) (map[string]claudeGuestQuota, error) { return nil, nil }
	a, b := list.Accounts[0].ID, list.Accounts[1].ID
	s.noteClaudeSelection(a, "/project")
	check := func(project, want string) {
		t.Helper()
		if id, err := s.allocateClaudeAccount(list, project, noReadings); err != nil || id.ID != want {
			t.Fatalf("project %q selected %q, want %q (%v)", project, id, want, err)
		}
	}
	check("/project", a)
	check("/other", b)
	if id := pickClaudeAccount(list, map[string]int{a: 1}, nil, a, a, time.Now()); id != b {
		t.Fatal("affinity overrode a busier terminal", id)
	}
	list.Accounts[0].Enabled = false
	if err := s.saveClaudeAccounts(list); err != nil {
		t.Fatal(err)
	}
	check("/project", b)
	list.Accounts[0].Enabled = true
	if err := s.saveClaudeAccounts(list); err != nil {
		t.Fatal(err)
	}
	s.claudeAccounts.recent["/project"] = claudeProjectPick{id: a, at: time.Now().Add(-2 * claudeProjectAffinity)}
	check("/project", b)
}

func TestClaudeDraftCancellationRespectsAdmissionsAndBoundIdentities(t *testing.T) {
	s := testServer(t)
	draft := strings.Repeat("a", 24)
	list := claudeAccountList{Accounts: []claudeAccount{{ID: draft, Label: "新账号", Status: "pending", Enabled: true}}}
	if err := s.saveClaudeAccounts(list); err != nil {
		t.Fatal(err)
	}
	cancel := func(want int) {
		t.Helper()
		w := request(s, "POST", "/api/claude-accounts", s.origin, `{"action":"cancel","id":"`+draft+`"}`, true)
		if w.Code != want {
			t.Fatalf("cancel got %d, want %d: %s", w.Code, want, w.Body)
		}
	}
	// A login has selected its profile but not yet published its terminal.
	s.op.Lock()
	cancel(409)
	s.op.Unlock()
	s.terminals.sessions = map[string]*terminalSession{"login": {claudeAccountID: draft, kind: "claude-login"}}
	cancel(409)
	delete(s.terminals.sessions, "login")
	s.claudeAccounts.mu.Lock()
	poll := s.claudeAccounts.claim(draft)
	s.claudeAccounts.mu.Unlock()
	cancel(409)
	s.claudeAccounts.finish(draft, poll, nil)
	cancel(200)
	cancel(200)
	if id, err := s.selectClaudeAccount("", false, ""); err != nil || id.ID != "" {
		t.Fatalf("cancelling the last draft did not restore existing behavior: %q %v", id, err)
	}
	list.Accounts[0].Email = "original@example.org"
	list.Accounts[0].Status = "signed_out"
	if err := s.saveClaudeAccounts(list); err != nil {
		t.Fatal(err)
	}
	cancel(409)
	kept, _ := s.loadClaudeAccounts()
	if kept.find(draft) == nil {
		t.Fatal("cancellation removed a previously bound identity")
	}
}
