package workbench

import (
	"reflect"
	"strings"
	"testing"
)

func TestCCSTerminalKeepsItsOwnIdentityWhenPoolExists(t *testing.T) {
	s := testServer(t)
	pool := claudeAccountList{Accounts: []claudeAccount{{ID: strings.Repeat("a", 24), Source: "managed", Label: "Existing", Enabled: true, Status: "ready", Email: "qa@example.test"}}}
	if err := s.saveClaudeAccounts(pool); err != nil {
		t.Fatal(err)
	}
	before, err := s.loadClaudeAccounts()
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []string{"", "auto"} {
		selected, err := s.selectTerminalClaudeAccount("ccs sample", request, false, s.workspace())
		if err != nil || selected != (claudeSelection{}) {
			t.Fatalf("CCS received a Cage pool identity: %#v %v", selected, err)
		}
	}
	after, err := s.loadClaudeAccounts()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("CCS selection changed the existing pool")
	}
}
func TestCCSTerminalRejectsAnExplicitCageAccount(t *testing.T) {
	s := testServer(t)
	if _, err := s.selectTerminalClaudeAccount("ccs sample", strings.Repeat("a", 24), false, s.workspace()); err == nil {
		t.Fatal("CCS accepted a Cage account override")
	}
}
