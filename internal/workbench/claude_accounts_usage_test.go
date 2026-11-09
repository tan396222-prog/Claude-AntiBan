package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func usageServer(t *testing.T, n int) (*Server, claudeAccountList) {
	t.Helper()
	s := testServer(t)
	s.ready = true
	t.Cleanup(func() { s.beginClose(); s.activities.Wait() })
	list := claudeAccountList{Accounts: []claudeAccount{}}
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("%024x", i)
		list.Accounts = append(list.Accounts, claudeAccount{ID: id, Source: "managed", Status: "ready", Email: id + "@example.org", Enabled: true})
	}
	if err := s.saveClaudeAccounts(list); err != nil {
		t.Fatal(err)
	}
	s.claudeAccounts.probe = func(_ context.Context, id string) (claudeGuestSnapshot, error) {
		if id == "default" {
			return claudeGuestSnapshot{}, nil
		}
		return claudeGuestSnapshot{LoggedIn: true, Email: id + "@example.org"}, nil
	}
	return s, list
}

func TestClaudeUsagePanelAndManualRefreshUseOfficialProbe(t *testing.T) {
	s, list := usageServer(t, 2)
	list.Accounts[1].Enabled = false // participation doesn't hide a subscription's quota.
	if err := s.saveClaudeAccounts(list); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	var mode atomic.Int32
	now := time.Now().Unix()
	s.claudeAccounts.usageProbe = func(_ context.Context, id, subject string) (claudeGuestSnapshot, error) {
		calls.Add(1)
		email := id + "@example.org"
		if claudeIdentityHash(email, "") != subject {
			t.Errorf("missing or misbound expected identity")
		}
		r := claudeGuestSnapshot{LoggedIn: true, Email: email}
		switch mode.Load() {
		case 0:
			r.UsageStatus = "reported"
			r.Quota = &claudeQuota{ObservedAt: now - 120, FiveHour: &claudeWindow{UsedPercentage: 40, ResetsAt: now + 3600}, SevenDay: &claudeWindow{UsedPercentage: 61, ResetsAt: now + 86400}}
		case 1:
			r.UsageStatus = "partial"
			r.Quota = &claudeQuota{ObservedAt: now - 30, FiveHour: &claudeWindow{UsedPercentage: 45}}
		case 2:
			return claudeGuestSnapshot{}, errors.New("simulated guest failure")
		case 3:
			r.UsageStatus = "unavailable"
		case 4:
			r.UsageStatus = "cached"
			r.Quota = &claudeQuota{ObservedAt: now - 200, FiveHour: &claudeWindow{UsedPercentage: 7, ResetsAt: now + 3600}}
		case 6:
			r.UsageStatus = "not_applicable"
		case 5:
			r.UsageStatus = "reported"
			r.Email = "wrong@example.org"
			r.Quota = &claudeQuota{ObservedAt: now, FiveHour: &claudeWindow{UsedPercentage: 99, ResetsAt: now + 999}}
		}
		return r, nil
	}
	panel := func(method, path, body string) claudePanel {
		t.Helper()
		w := request(s, method, path, s.origin, body, true)
		var v claudePanel
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		s.activities.Wait()
		return v
	}
	panel("GET", "/api/claude-accounts", "")
	panel("GET", "/api/claude-accounts?panel=visible", "")
	if calls.Load() != 0 {
		t.Fatal("passive GET ran /usage")
	}
	panel("POST", "/api/claude-accounts", `{"action":"usage_panel"}`)
	if calls.Load() != 2 {
		t.Fatalf("opening a panel checked %d accounts", calls.Load())
	}
	panel("POST", "/api/claude-accounts", `{"action":"usage_panel"}`)
	panel("GET", "/api/claude-accounts?panel=visible", "")
	if calls.Load() != 2 {
		t.Fatal("reopen/read amplified upstream traffic")
	}
	body := `{"action":"refresh","id":"` + list.Accounts[0].ID + `"}`
	for i, status := range []string{"partial", "error", "unavailable", "cached", "identity_changed", "not_applicable"} {
		mode.Store(int32(i + 1))
		v := panel("POST", "/api/claude-accounts", body)
		a := v.Accounts[0]
		if a.UsageStatus != status || a.UsageCheckedAt < now || a.Quota.FiveHour.UsedPercentage != 45 || a.Quota.FiveHour.ObservedAt != now-30 || a.Quota.SevenDay.ObservedAt != now-120 || a.Quota.SevenDay.UsedPercentage != 61 {
			t.Fatalf("%s replaced the last valid observation: %+v %+v", status, a, a.Quota)
		}
		if v.Accounts[1].UsageStatus != "reported" || v.Accounts[1].Quota.FiveHour.UsedPercentage != 40 {
			t.Fatal("manual refresh affected unrelated account")
		}
	}
}

func TestClaudeUsageUnregisteredDefaultRequiresTheDisplayedIdentity(t *testing.T) {
	s, _ := usageServer(t, 0)
	a := claudeGuestSnapshot{LoggedIn: true, Email: "a@example.org", Organization: "team"}
	current := a
	s.claudeAccounts.probe = func(context.Context, string) (claudeGuestSnapshot, error) { return current, nil }
	calls := 0
	s.claudeAccounts.usageProbe = func(_ context.Context, id, subject string) (claudeGuestSnapshot, error) {
		calls++
		if id != "default" || subject != a.subject() {
			t.Error("unexpected default identity")
		}
		r := a
		r.UsageStatus = "partial"
		r.Quota = &claudeQuota{ObservedAt: time.Now().Unix() - 20, FiveHour: &claudeWindow{UsedPercentage: 58}}
		return r, nil
	}
	wOpen := request(s, "POST", "/api/claude-accounts", s.origin, `{"action":"usage_panel"}`, true)
	if wOpen.Code != 200 {
		t.Fatal(wOpen.Code, wOpen.Body)
	}
	s.activities.Wait()
	if calls != 1 {
		t.Fatal("panel did not proactively check detected, unregistered default")
	}
	body := `{"action":"refresh_default","identity":"` + a.subject() + `"}`
	w := request(s, "POST", "/api/claude-accounts", s.origin, body, true)
	var v claudePanel
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil || len(v.Accounts) != 0 || v.Detected == nil || v.Detected.Quota == nil || v.Detected.Quota.FiveHour.UsedPercentage != 58 || v.Detected.UsageStatus != "partial" {
		t.Fatal(w.Code, w.Body.String())
	}
	current = claudeGuestSnapshot{LoggedIn: true, Email: "b@example.org"}
	if w = request(s, "POST", "/api/claude-accounts", s.origin, body, true); w.Code != 409 || calls != 2 {
		t.Fatal("stale A triggered usage for B", w.Code, calls)
	}
	v, _, err := s.claudePanelView(context.Background())
	if err != nil || v.Detected == nil || v.Detected.Quota != nil || v.Detected.UsageStatus != "" {
		t.Fatal("B inherited A's readings", v, err)
	}
	current = a
	if err := s.joinClaudeDefault(context.Background(), a.subject()); err != nil {
		t.Fatal(err)
	}
	saved, _ := s.loadClaudeAccounts()
	if saved.Accounts[0].Quota == nil || saved.Accounts[0].Quota.FiveHour.UsedPercentage != 58 || saved.Accounts[0].UsageStatus != "partial" {
		t.Fatal("joining lost this identity's last reading")
	}
}

func TestClaudeUsageBatchBoundedAndCancellable(t *testing.T) {
	for _, how := range []string{"lock", "stop"} {
		t.Run(how, func(t *testing.T) {
			s, list := usageServer(t, 40)
			entered := make(chan string, 40)
			release := make(chan struct{})
			var calls atomic.Int32
			s.claudeAccounts.usageProbe = func(ctx context.Context, id, subj string) (claudeGuestSnapshot, error) {
				calls.Add(1)
				entered <- id
				select {
				case <-ctx.Done():
					return claudeGuestSnapshot{}, ctx.Err()
				case <-release:
				}
				return claudeGuestSnapshot{LoggedIn: true, Email: id + "@example.org", UsageStatus: "unavailable"}, nil
			}
			open := func() {
				w := request(s, "POST", "/api/claude-accounts", s.origin, `{"action":"usage_panel"}`, true)
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body)
				}
			}
			open()
			if id := <-entered; id != list.Accounts[0].ID {
				t.Fatal("wrong account at queue start")
			}
			open()
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
			defer cancel()
			if err := s.refreshClaudeUsage(ctx, claudeAccountInput{Action: "refresh", ID: list.Accounts[0].ID}); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("same-account check did not join existing call", err)
			}
			second := make(chan error, 1)
			go func() {
				second <- s.refreshClaudeUsage(context.Background(), claudeAccountInput{Action: "refresh", ID: list.Accounts[1].ID})
			}()
			<-entered
			body := `{"action":"refresh","id":"` + list.Accounts[2].ID + `"}`
			if w := request(s, "POST", "/api/claude-accounts", s.origin, body, true); w.Code != http.StatusConflict {
				t.Fatal("per-account probes exceeded global limit", w.Code)
			}
			if how == "lock" {
				s.account.lock()
			} else {
				s.mu.Lock()
				s.ready = false
				s.mu.Unlock()
			}
			close(release)
			<-second
			s.activities.Wait()
			got, _ := s.loadClaudeAccounts()
			if calls.Load() != 2 {
				t.Fatalf("after %s, started %d commands", how, calls.Load())
			}
			for _, a := range got.Accounts {
				if a.UsageCheckedAt != 0 {
					t.Fatal("wrote late results after", how)
				}
			}
		})
	}
}

func TestClaudeUnknownResetOnlyBlocksWhileFresh(t *testing.T) {
	now := time.Now()
	a := claudeAccount{Quota: &claudeQuota{FiveHour: &claudeWindow{UsedPercentage: 100, ObservedAt: now.Unix()}}}
	if _, ok := a.headroom(now); ok {
		t.Fatal("ignored exhausted window without a known reset")
	}
	if room, ok := a.headroom(now.Add(claudeQuotaFresh + time.Second)); !ok || room != -1 {
		t.Fatal("treated missing reset as permanent exhaustion", room, ok)
	}
}

func TestClaudeUsageHiddenPanelStopsDispatching(t *testing.T) {
	s, _ := usageServer(t, 4)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s.claudeAccounts.usageProbe = func(_ context.Context, id, subject string) (claudeGuestSnapshot, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return claudeGuestSnapshot{LoggedIn: true, Email: id + "@example.org", UsageStatus: "unavailable"}, nil
	}
	w := request(s, "POST", "/api/claude-accounts", s.origin, `{"action":"usage_panel"}`, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	<-entered
	s.claudeAccounts.mu.Lock()
	s.claudeAccounts.panelUntil = time.Now().Add(-time.Second)
	s.claudeAccounts.mu.Unlock()
	// A workspace GET must neither renew the lease nor start another batch.
	w = request(s, "GET", "/api/claude-accounts", "", "", true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	close(release)
	s.activities.Wait()
	if calls.Load() != 1 {
		t.Fatal("hidden panel kept issuing upstream queries", calls.Load())
	}
}

func TestClaudeUsageKeepsExpiredHistoricalWindowsForBothSources(t *testing.T) {
	s, list := usageServer(t, 1)
	now := time.Now()
	history := &claudeQuota{ObservedAt: now.Add(-time.Hour).Unix(), FiveHour: &claudeWindow{UsedPercentage: 100, ResetsAt: now.Add(-time.Minute).Unix()}, SevenDay: &claudeWindow{UsedPercentage: 63, ResetsAt: now.Add(time.Hour).Unix()}}
	list.Accounts[0].mergeQuota(history, now)
	if err := s.saveClaudeAccounts(list); err != nil {
		t.Fatal(err)
	}
	candidate := claudeGuestSnapshot{LoggedIn: true, Email: "default@example.org", Quota: history}
	// Only a passive CLI snapshot has ever reported this unregistered account.
	s.claudeAccounts.probe = func(_ context.Context, id string) (claudeGuestSnapshot, error) {
		if id == "default" {
			return candidate, nil
		}
		return claudeGuestSnapshot{LoggedIn: true, Email: list.find(id).Email}, nil
	}
	if _, _, err := s.claudePanelView(context.Background()); err != nil {
		t.Fatal(err)
	}
	candidate.Quota = nil
	s.claudeAccounts.usageProbe = func(_ context.Context, id, expected string) (claudeGuestSnapshot, error) {
		if id == "default" {
			return claudeGuestSnapshot{LoggedIn: true, Email: candidate.Email, UsageStatus: "unavailable"}, nil
		}
		return claudeGuestSnapshot{LoggedIn: true, Email: list.find(id).Email, UsageStatus: "unavailable"}, nil
	}
	for _, body := range []string{`{"action":"refresh","id":"` + list.Accounts[0].ID + `"}`, `{"action":"refresh_default","identity":"` + candidate.subject() + `"}`} {
		w := request(s, "POST", "/api/claude-accounts", s.origin, body, true)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		s.activities.Wait()
	}
	view, _, err := s.claudePanelView(context.Background())
	if err != nil || view.Detected == nil {
		t.Fatal(view, err)
	}
	for _, q := range []*claudeQuota{view.Accounts[0].Quota, view.Detected.Quota} {
		if q == nil || q.FiveHour == nil || q.SevenDay == nil || q.FiveHour.UsedPercentage != 100 || q.FiveHour.ObservedAt != history.ObservedAt || q.SevenDay.ObservedAt != history.ObservedAt {
			t.Fatal("missing read discarded or retimestamped historical quota", q)
		}
		if _, eligible := (claudeAccount{Quota: q}).headroom(now); !eligible {
			t.Fatal("closed 5h window still blocked allocation")
		}
	}
}

func TestClaudeUsageFailedBatchOnlyRetriesOnOpeningAfterDebounce(t *testing.T) {
	s, list := usageServer(t, 1)
	var calls atomic.Int32
	s.claudeAccounts.usageProbe = func(context.Context, string, string) (claudeGuestSnapshot, error) {
		calls.Add(1)
		return claudeGuestSnapshot{}, errors.New("unavailable")
	}
	get := func() claudePanel {
		w := request(s, "GET", "/api/claude-accounts?panel=visible", "", "", true)
		s.activities.Wait()
		var v claudePanel
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil {
			t.Fatal(w.Code, w.Body)
		}
		return v
	}
	open := func() claudePanel {
		w := request(s, "POST", "/api/claude-accounts", s.origin, `{"action":"usage_panel"}`, true)
		s.activities.Wait()
		var v claudePanel
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil {
			t.Fatal(w.Code, w.Body)
		}
		return v
	}
	open()
	if v := get(); v.UsagePanelRunning || v.Accounts[0].UsageStatus != "error" || v.Accounts[0].UsageChecking {
		t.Fatal("failed batch never ended", v)
	}
	if v := open(); v.UsagePanelRunning || calls.Load() != 1 {
		t.Fatal("recent failure started an empty batch")
	}
	list, _ = s.loadClaudeAccounts()
	list.Accounts[0].UsageCheckedAt = time.Now().Add(-61 * time.Second).Unix()
	if err := s.saveClaudeAccounts(list); err != nil {
		t.Fatal(err)
	}
	if v := get(); v.UsagePanelRunning || calls.Load() != 1 {
		t.Fatal("ordinary visible poll restarted an expired failed batch")
	}
	open()
	if calls.Load() != 2 || get().UsagePanelRunning {
		t.Fatal("did not retry on the next real panel open")
	}
}
