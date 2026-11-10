package workbench

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestClaudeAutomaticSwitchRequiresQuotaAndConsent(t *testing.T) {
	s, id, _ := metadataFixture(t)
	s.ready = true
	a, b := strings.Repeat("a", 24), strings.Repeat("b", 24)
	list := claudeAccountList{Accounts: []claudeAccount{{ID: a, Label: "A", Email: "a@example.test", Enabled: true, Status: "ready", HandoffGroup: "personal"}, {ID: b, Label: "B", Email: "b@example.test", Enabled: true, Status: "ready", HandoffGroup: "personal"}}}
	if err := s.saveClaudeAccounts(list); err != nil {
		t.Fatal(err)
	}
	record, _ := s.mobileCLI.record(s.profile, id)
	record.Launcher = "claude"
	record.Account = claudeSelection{a, a, claudeIdentityHash("a@example.test", "")}
	epoch, _ := s.account.access()
	ctx := context.WithValue(context.Background(), mobileEpochKey{}, epoch)
	for _, scenario := range []struct{ policy, failure, reason string }{{"ask", "rate_limit", "手动确认"}, {"allow", "overloaded", "手动确认"}, {"allow", "rate_limit", "尚未确认"}} {
		record.SwitchPolicy, record.AccountFailure = scenario.policy, scenario.failure
		if err := writePrivate(mobileRecordPath(s.profile, id), record); err != nil {
			t.Fatal(err)
		}
		err := s.switchClaudeAccount(ctx, id, claudeSwitchInput{SourceID: record.CLIID, Target: b}, true)
		if err == nil || !strings.Contains(err.Error(), scenario.reason) {
			t.Fatal(scenario, err)
		}
		stored, _ := s.mobileCLI.record(s.profile, id)
		if stored.Switch != nil || stored.Account.ID != a {
			t.Fatal("preflight changed ownership")
		}
	}
}

func TestClaudeSchedulingModesAndPersistence(t *testing.T) {
	s := testServer(t)
	a, b := strings.Repeat("a", 24), strings.Repeat("b", 24)
	list := claudeAccountList{Accounts: []claudeAccount{{ID: a, Label: "A", Enabled: true, Status: "ready", Email: "a@example.test"}, {ID: b, Label: "B", Enabled: true, Status: "ready", Email: "b@example.test"}}}
	if err := s.saveClaudeAccounts(list); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.loadClaudeAccounts()
	if err != nil || loaded.Scheduling.Mode != "balanced" || loaded.Scheduling.SwitchPolicy != "ask" {
		t.Fatal("legacy defaults", loaded, err)
	}
	if id := pickClaudeAccount(loaded, map[string]int{a: 2}, nil, "", "", time.Now()); id != b {
		t.Fatal("balance ignored occupancy", id)
	}
	response := request(s, "POST", "/api/claude-accounts", s.origin, `{"action":"scheduling","scheduling":{"mode":"sequential","switch_policy":"allow"},"order":["`+b+`","`+a+`"]}`, true)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	loaded, err = s.loadClaudeAccounts()
	if err != nil || loaded.Scheduling.Mode != "sequential" || loaded.Accounts[0].ID != b {
		t.Fatal(loaded, err)
	}
	if id := pickClaudeAccount(loaded, map[string]int{b: 3}, nil, "", "", time.Now()); id != b {
		t.Fatal("sequential did not keep priority", id)
	}
	loaded.Accounts[0].Quota = &claudeQuota{FiveHour: &claudeWindow{UsedPercentage: 100, ResetsAt: time.Now().Add(time.Hour).Unix(), ObservedAt: time.Now().Unix()}}
	if id := pickClaudeAccount(loaded, nil, nil, "", "", time.Now()); id != a {
		t.Fatal("exhausted account selected", id)
	}
	response = request(s, "POST", "/api/claude-accounts", s.origin, `{"action":"scheduling","scheduling":{"mode":"sequential","switch_policy":"allow"},"order":["`+b+`","`+b+`"]}`, true)
	if response.Code == 200 {
		t.Fatal("duplicate order accepted")
	}
}

func TestClaudeSwitchGroupAndDurableBinding(t *testing.T) {
	a, b := strings.Repeat("a", 24), strings.Repeat("b", 24)
	list := claudeAccountList{Accounts: []claudeAccount{{ID: a, Label: "A", Email: "a@example.test", Enabled: true, Status: "ready"}, {ID: b, Label: "B", Email: "b@example.test", Enabled: true, Status: "ready"}}}
	record := &mobileRecord{Account: claudeSelection{ID: a, GuestID: a, Subject: claudeIdentityHash("a@example.test", "")}}
	if len(switchCandidates(list, record)) != 0 {
		t.Fatal("ungrouped history escaped")
	}
	for i := range list.Accounts {
		list.Accounts[i].HandoffGroup = "personal"
	}
	if got := switchCandidates(list, record); len(got) != 1 || got[0].ID != b {
		t.Fatal(got)
	}
	list.Accounts[1].HandoffGroup = "company"
	if len(switchCandidates(list, record)) != 0 {
		t.Fatal("organization boundary escaped")
	}
	list.Accounts[1].HandoffGroup = "personal"
	record.Account.Subject = "changed"
	if len(switchCandidates(list, record)) != 0 {
		t.Fatal("stale source identity accepted")
	}
	record.Switch = &claudeAccountSwitch{ID: "receipt", Phase: "starting", Source: claudeExecution{Config: "private-config"}, Target: claudeSelection{Subject: "private-subject"}}
	raw, _ := json.Marshal(record.snapshot())
	if strings.Contains(string(raw), "private-") {
		t.Fatal("internal authentication binding exposed", string(raw))
	}
}
