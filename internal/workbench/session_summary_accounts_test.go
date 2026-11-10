package workbench

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestSessionSummaryAccountAfterRestartAndSwitch(t *testing.T) {
	profile, workspace := t.TempDir(), t.TempDir()
	id := uuid.NewString()
	// Older records store the binding in Account without an account_id in the summary.
	record := &mobileRecord{
		mobileSession: mobileSession{
			mobileSummary: mobileSummary{ID: id, Workspace: workspace, Title: "对话"},
			Turns:         []mobileTurn{},
		},
		CLIID: uuid.NewString(), Account: claudeSelection{ID: "first-account"},
	}
	if err := writePrivate(mobileRecordPath(profile, id), record); err != nil {
		t.Fatal(err)
	}
	store := &mobileCLIStore{}
	granted := map[string]bool{workspace: true}
	check := func(want string) {
		t.Helper()
		list, err := store.summaries(profile, granted, false)
		if err != nil || len(list) != 1 {
			t.Fatalf("summaries: %v, %v", list, err)
		}
		data, err := json.Marshal(list[0])
		if err != nil {
			t.Fatal(err)
		}
		var view map[string]any
		if err := json.Unmarshal(data, &view); err != nil {
			t.Fatal(err)
		}
		if view["account_id"] != want {
			t.Fatalf("account_id = %v, want %s", view["account_id"], want)
		}
	}
	check("first-account")
	// A running conversation must report its current binding, rather than the cached one.
	record.Account.ID = "second-account"
	store.active = map[string]*mobileRun{id: {record: record}}
	check("second-account")
	if err := writePrivate(mobileRecordPath(profile, id), record); err != nil {
		t.Fatal(err)
	}
	store = &mobileCLIStore{}
	check("second-account")
}

func TestSessionSummaryRunningRequiresFreshProcessObservation(t *testing.T) {
	profile, workspace, id := t.TempDir(), t.TempDir(), uuid.NewString()
	record := &mobileRecord{mobileSession: mobileSession{mobileSummary: mobileSummary{
		ID: id, Workspace: workspace, Running: true,
	}, Turns: []mobileTurn{}}, Terminal: true, CLIID: uuid.NewString()}
	if err := writePrivate(mobileRecordPath(profile, id), record); err != nil {
		t.Fatal(err)
	}
	store := &mobileCLIStore{}
	check := func(want bool) {
		t.Helper()
		list, err := store.summaries(profile, map[string]bool{workspace: true}, false)
		if err != nil || len(list) != 1 || list[0].Running != want {
			t.Fatalf("running = %v, want %v; error = %v", list, want, err)
		}
	}
	check(false) // Persisted activity cannot disable a session after restart.
	store.running = map[string]bool{id: true}
	check(true) // An idle external process is running even without a busy turn.
	store.active = map[string]*mobileRun{id: {record: record}}
	check(true)
	store.running = map[string]bool{}
	check(false)
}
