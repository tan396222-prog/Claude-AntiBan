package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestClaudeDefaultDiscoveryJoinAndLiveIdentity(t *testing.T) {
	s := testServer(t)
	s.ready = true
	at := time.Now().Unix()
	a := claudeGuestSnapshot{LoggedIn: true, Email: "a@example.org", Organization: "personal", Quota: &claudeQuota{ObservedAt: at, FiveHour: &claudeWindow{UsedPercentage: 37, ResetsAt: at + 300}}}
	b := claudeGuestSnapshot{LoggedIn: true, Email: "b@example.org", Organization: "work"}
	current := a
	var unavailable bool
	var probes int
	s.claudeAccounts.probe = func(_ context.Context, id string) (claudeGuestSnapshot, error) {
		if id != "default" {
			t.Errorf("unexpected probe %q", id)
		}
		probes++
		if unavailable {
			return claudeGuestSnapshot{}, errors.New("SSH unavailable")
		}
		return current, nil
	}
	panel := func() claudePanel {
		t.Helper()
		w := request(s, "GET", "/api/claude-accounts", "", "", true)
		var v claudePanel
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil {
			t.Fatal(w.Code, w.Body)
		}
		return v
	}
	join := func(person claudeGuestSnapshot, status int) {
		t.Helper()
		w := request(s, "POST", "/api/claude-accounts", s.origin, `{"action":"join","identity":"`+person.subject()+`"}`, true)
		if w.Code != status {
			t.Fatalf("join got %d, want %d (%s)", w.Code, status, w.Body)
		}
	}
	if v := panel(); v.DetectionStatus != "ready" || v.Detected == nil || v.Detected.Email != a.Email || len(v.Accounts) != 0 {
		t.Fatal(v)
	}
	_ = panel()
	if probes != 1 {
		t.Fatal("panel did not cache initial probe", probes)
	}
	if _, err := os.Stat(filepath.Join(s.profile, "claude-accounts.json")); !os.IsNotExist(err) {
		t.Fatal("discovery enrolled or wrote an identity")
	}
	current = b
	join(a, 409)
	current = a
	join(a, 200)
	choice, err := s.selectClaudeAccount("", false, "/p")
	if err != nil || choice.Subject != a.subject() || choice.GuestID != "default" || !validClaudeID(choice.ID) {
		t.Fatal(choice, err)
	}
	current = b
	if _, err = s.selectClaudeAccount("", false, "/p"); err == nil {
		t.Fatal("selected a default identity that was no longer present")
	}
	if v := panel(); v.Detected == nil || v.Detected.Identity != b.subject() || len(v.Accounts) != 1 || v.Accounts[0].Status != "changed" {
		t.Fatal(v)
	}
	join(b, 200)
	joined, err := s.selectClaudeAccount("", false, "/p")
	if err != nil || joined.ID == choice.ID || joined.Subject != b.subject() || choice.Subject != a.subject() {
		t.Fatalf("selection lost the original snapshot: %+v %+v %v", choice, joined, err)
	}
	if v := panel(); v.Detected != nil || len(v.Accounts) != 2 || v.Accounts[1].Quota != nil {
		t.Fatalf("inherited A's quota or registered B twice: %+v", v)
	}
	unavailable = true
	if _, err = s.selectClaudeAccount("", false, "/p"); err == nil {
		t.Fatal("stale default identity passed a failed live check")
	}
	if v := panel(); v.DetectionStatus != "unavailable" || v.DetectionError == "" || v.Detected != nil {
		t.Fatal(v)
	}
	if _, err = s.selectClaudeAccount(choice.ID, true, ""); err == nil {
		t.Fatal("login tool could mutate default credentials")
	}
}

func TestClaudeDefaultManagedDuplicateAndFallback(t *testing.T) {
	s := testServer(t)
	read := claudeGuestSnapshot{LoggedIn: true, Email: "managed@example.org", Organization: "team"}
	s.claudeAccounts.probe = func(context.Context, string) (claudeGuestSnapshot, error) { return read, nil }
	list := claudeAccountList{Accounts: []claudeAccount{
		{ID: strings.Repeat("a", 24), Source: "managed", Email: read.Email, Organization: read.Organization, Enabled: true, Status: "ready"},
		{ID: strings.Repeat("b", 24), Source: "default", Email: "old@example.org", Organization: "team", Enabled: true, Status: "ready"},
	}}
	if err := s.saveClaudeAccounts(list); err != nil {
		t.Fatal(err)
	}
	s.ready = true
	v, _, err := s.claudePanelView(context.Background())
	if err != nil || v.Detected != nil || v.Accounts[1].Status != "changed" {
		t.Fatal(v, err)
	}
	if err = s.joinClaudeDefault(context.Background(), read.subject()); err != nil {
		t.Fatal(err)
	}
	noReadings := func(context.Context, []string) (map[string]claudeGuestQuota, error) { return nil, nil }
	selection, err := s.allocateClaudeAccount(list, "", noReadings)
	if err != nil || selection.ID != list.Accounts[0].ID || selection.GuestID != selection.ID || selection.Subject != read.subject() {
		t.Fatal(selection, err)
	}
	got, _ := s.loadClaudeAccounts()
	if len(got.Accounts) != 2 {
		t.Fatal("second copy of the same subscribed identity")
	}
}

func TestClaudeOneStepLoginSurvivesLockAndRestart(t *testing.T) {
	s := testServer(t)
	var current claudeGuestSnapshot
	s.claudeAccounts.probe = func(context.Context, string) (claudeGuestSnapshot, error) { return current, nil }
	// The first request only reserves a private directory for the official CLI.
	login, err := s.selectClaudeAccount("", true, "")
	if err != nil || !validClaudeID(login.ID) || login.GuestID != login.ID {
		t.Fatal(login, err)
	}
	retry, err := s.selectClaudeAccount("", true, "")
	if err != nil || login != retry {
		t.Fatal("retry created another profile", login, retry, err)
	}
	list, _ := s.loadClaudeAccounts()
	if len(list.Accounts) != 0 || list.PendingLogin != login.ID {
		t.Fatal("pending attempt exposed an account")
	}
	if legacy, err := s.selectClaudeAccount("", false, ""); err != nil || legacy.ID != "" {
		t.Fatal("pending login blocked existing workflow", legacy, err)
	}
	s.account.lock()
	current = claudeGuestSnapshot{LoggedIn: true, Email: "real@example.org", Organization: "work", Plan: "pro"}
	if err = s.confirmClaudeLogin(context.Background(), login.ID); err == nil {
		t.Fatal("registered while locked")
	}
	s.account.setKey(make([]byte, 32))
	restarted := &Server{profile: s.profile, account: s.account, ready: true}
	restarted.claudeAccounts.probe = func(_ context.Context, id string) (claudeGuestSnapshot, error) {
		if id == "default" {
			return claudeGuestSnapshot{}, nil
		}
		if id != login.ID {
			t.Fatal("opened wrong profile", id)
		}
		return current, nil
	}
	v, _, err := restarted.claudePanelView(context.Background())
	if err != nil || len(v.Accounts) != 1 || v.Accounts[0].ID != login.ID || v.Accounts[0].Label != current.Email || !v.Accounts[0].Enabled || v.Accounts[0].Status != "ready" || v.Accounts[0].Source != "managed" {
		t.Fatalf("successful login was not recovered: %+v %v", v, err)
	}
	list, _ = restarted.loadClaudeAccounts()
	if list.PendingLogin != "" {
		t.Fatal("finished enrollment retained pending marker")
	}
	if err = restarted.confirmClaudeLogin(context.Background(), login.ID); err != nil {
		t.Fatal(err)
	}
	list, _ = restarted.loadClaudeAccounts()
	if len(list.Accounts) != 1 {
		t.Fatal("repeated completion registered twice")
	}
}

func TestClaudeUnsuccessfulLoginAndDuplicateStayOutsidePool(t *testing.T) {
	s := testServer(t)
	var current claudeGuestSnapshot
	s.claudeAccounts.probe = func(context.Context, string) (claudeGuestSnapshot, error) { return current, nil }
	login, err := s.selectClaudeAccount("", true, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.confirmClaudeLogin(context.Background(), login.ID); err != nil {
		t.Fatal(err)
	}
	list, _ := s.loadClaudeAccounts()
	if len(list.Accounts) != 0 || list.PendingLogin != "" {
		t.Fatalf("cancelled login became a profile: %+v", list)
	}
	current = claudeGuestSnapshot{LoggedIn: true, Email: "same@example.org"}
	original := strings.Repeat("1", 24)
	list.Accounts = append(list.Accounts, claudeAccount{ID: original, Source: "managed", Email: current.Email, Enabled: false, Status: "ready"})
	if err = s.saveClaudeAccounts(list); err != nil {
		t.Fatal(err)
	}
	login, err = s.selectClaudeAccount("", true, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.confirmClaudeLogin(context.Background(), login.ID); err != nil {
		t.Fatal(err)
	}
	list, _ = s.loadClaudeAccounts()
	if list.PendingLogin != "" || len(list.Accounts) != 1 || list.Accounts[0].Enabled || list.Accounts[0].ID != original {
		t.Fatal("duplicate login altered membership", list)
	}
}

func TestClaudeDefaultConcurrentDiscoverySingleProbe(t *testing.T) {
	s := testServer(t)
	s.ready = true
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	s.claudeAccounts.probe = func(context.Context, string) (claudeGuestSnapshot, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return claudeGuestSnapshot{}, nil
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, _, err := s.claudePanelView(context.Background())
			if err != nil || v.DetectionStatus != "signed_out" {
				t.Error(v, err)
			}
		}()
	}
	<-entered
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("concurrent panel refresh duplicated a guest probe")
	}
	s.guestUpdateRequired = true
	v, _, err := s.claudePanelView(context.Background())
	if err != nil || v.DetectionStatus != "update_required" || v.DetectionError == "" {
		t.Fatal(v, err)
	}
	if w := request(s, "POST", "/api/claude-accounts", s.origin, `{"action":"create","label":"phantom"}`, true); w.Code == http.StatusOK {
		t.Fatal("registration without real login accepted")
	}
}

func TestClaudeIndependentLoginKeepsDefaultIdentityAndMakesBothAccountsAvailable(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	a := claudeGuestSnapshot{LoggedIn: true, Email: "a@example.org", Organization: "home"}
	b := claudeGuestSnapshot{LoggedIn: true, Email: "b@example.org", Organization: "work"}
	current := a
	var independent string
	s.claudeAccounts.probe = func(_ context.Context, id string) (claudeGuestSnapshot, error) {
		switch id {
		case "default":
			return current, nil
		case independent:
			return a, nil
		default:
			return claudeGuestSnapshot{}, errors.New("wrong configuration directory")
		}
	}
	if err := s.joinClaudeDefault(ctx, a.subject()); err != nil {
		t.Fatal(err)
	}
	list, _ := s.loadClaudeAccounts()
	stable := list.bySubject(a.subject()).ID
	list.Accounts[0].Label, list.Accounts[0].Enabled = "个人空间", false
	if err := s.saveClaudeAccounts(list); err != nil {
		t.Fatal(err)
	}
	current = b
	if err := s.joinClaudeDefault(ctx, b.subject()); err != nil {
		t.Fatal(err)
	}
	next, err := s.selectClaudeAccount("", true, "")
	if err != nil {
		t.Fatal(err)
	}
	independent = next.GuestID
	if err = s.confirmClaudeLogin(ctx, next.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.confirmClaudeLogin(ctx, next.ID); err != nil {
		t.Fatal("duplicate completion probed the wrong directory", err)
	}
	list, err = s.loadClaudeAccounts()
	old := list.find(stable)
	if err != nil || len(list.Accounts) != 2 || old == nil || old.Source != "managed" || old.ProfileID != independent || old.Enabled || old.Label != "个人空间" || old.Status != "ready" {
		t.Fatal(list, err)
	}
	// Re-enabling A must allow new work on both subscriptions simultaneously.
	old.Enabled = true
	if err = s.saveClaudeAccounts(list); err != nil {
		t.Fatal(err)
	}
	read := func(_ context.Context, ids []string) (map[string]claudeGuestQuota, error) {
		if len(ids) != 1 || ids[0] != independent {
			t.Errorf("wrong guest quota directories: %v", ids)
		}
		return nil, nil
	}
	first, err := s.allocateClaudeAccount(list, "", read)
	if err != nil || first.ID != stable || first.GuestID != independent || first.Subject != a.subject() {
		t.Fatal(first, err)
	}
	s.noteClaudeSelection(first.ID, "")
	second, err := s.allocateClaudeAccount(list, "", read)
	if err != nil || second.ID == stable || second.GuestID != "default" || second.Subject != b.subject() {
		t.Fatal(second, err)
	}
	again, err := s.selectClaudeAccount(stable, true, "")
	if err != nil || again.GuestID != independent {
		t.Fatal(again, err)
	}
}
