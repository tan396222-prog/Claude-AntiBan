package workbench

import "testing"

func TestAdoptedMobileTurns(t *testing.T) {
	legacy := []mobileTurn{{ID: "failed", Prompt: "failed first", Status: "failed", Error: "not started"},
		{ID: "mobile", Prompt: "hello", Text: "old partial", Status: "completed"}}
	current := []mobileTurn{{ID: "cli", Prompt: "hello", Text: "complete", Status: "completed"},
		{ID: "new", Prompt: "next", Status: "running"}}
	got := adoptedMobileTurns(legacy, current)
	if len(got) != 3 || got[0].ID != "failed" || got[1].ID != "mobile" || got[1].Text != "complete" || got[2].ID != "new" {
		t.Fatalf("history or request identity lost: %#v", got)
	}
	// A channel preparing its first frame must not erase cached history.
	if got := adoptedMobileTurns(legacy, nil); len(got) != 2 || got[1].Text != "old partial" {
		t.Fatal(got)
	}
}
