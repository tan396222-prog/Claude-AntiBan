//go:build linux || darwin || windows

package reversesshfs

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

type guardCall struct {
	rel   string
	entry bool
}

// A guest builds a guarded file's parent folder elsewhere and moves it into place: Guard is told
// that the names are being created, moved, linked or removed (entry), so it can refuse the
// parent folder of a guarded path, which a write-only check on the path would not see.
func TestRootedGuardParents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no symlinks here and asks with normalized names")
	}
	var calls []guardCall
	allow := false
	Guard = func(root, rel string, entry bool) bool {
		calls = append(calls, guardCall{rel, entry})
		parts := strings.Split(strings.ToLower(rel), "/")
		last := parts[len(parts)-1]
		if allow {
			return true
		}
		if last == ".vscode" && entry {
			return false
		}
		return !(last == "tasks.json" && len(parts) > 1 && parts[len(parts)-2] == ".vscode")
	}
	t.Cleanup(func() { Guard = nil })
	c, root := setupRooted(t, false)
	p := func(s string) string { return filepath.Join(root, s) }
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(p(".vscode"), 0o755))
	must(os.WriteFile(p(".vscode/tasks.json"), []byte("orig"), 0o644))
	must(c.Mkdir(p("evil")))
	f, err := c.Create(p("evil/tasks.json"))
	must(err)
	_, _ = f.Write([]byte("pwn"))
	must(f.Close())
	if !slices.Contains(calls, guardCall{"evil/tasks.json", false}) {
		t.Errorf("a write is not an entry: %v", calls)
	}

	for _, op := range []struct {
		name string
		rel  string
		f    func() error
	}{
		{"rename onto the parent", ".vscode", func() error { return c.PosixRename(p("evil"), p(".vscode")) }},
		{"rename onto the parent, no replace", ".vscode", func() error { return c.Rename(p("evil"), p(".vscode")) }},
		{"symlink as the parent", ".vscode", func() error { return c.Symlink("evil", p(".vscode")) }},
		{"rename the parent away", ".vscode", func() error { return c.PosixRename(p(".vscode"), p("old")) }},
		{"remove the parent", ".vscode", func() error { return c.RemoveDirectory(p(".vscode")) }},
		{"hard link to the file", ".vscode/tasks.json", func() error { return c.Link(p(".vscode/tasks.json"), p("evil/alias")) }},
		{"mkdir the parent", "sub/.vscode", func() error { return c.Mkdir(p("sub/.vscode")) }},
	} {
		if op.rel == "sub/.vscode" {
			must(os.Mkdir(p("sub"), 0o755))
		}
		calls = nil
		if err := op.f(); err == nil {
			t.Errorf("%s: allowed though Guard said no", op.name)
		}
		if !slices.Contains(calls, guardCall{op.rel, true}) {
			t.Errorf("%s: Guard asked %v, not %q as an entry", op.name, calls, op.rel)
		}
	}
	if b, _ := os.ReadFile(p(".vscode/tasks.json")); string(b) != "orig" {
		t.Errorf("tasks.json: %q", b)
	}
	if fi, err := os.Lstat(p(".vscode")); err != nil || !fi.IsDir() {
		t.Errorf(".vscode: %v, %v", fi, err)
	}
	if _, err := os.Lstat(p("evil/alias")); err == nil {
		t.Error("hard link made")
	}

	// With Guard saying yes, the same moves work: they were refused by Guard alone.
	allow = true
	must(c.PosixRename(p(".vscode"), p("old")))
	must(c.PosixRename(p("evil"), p(".vscode")))
	if b, _ := os.ReadFile(p(".vscode/tasks.json")); string(b) != "pwn" {
		t.Errorf("tasks.json: %q", b)
	}
}
