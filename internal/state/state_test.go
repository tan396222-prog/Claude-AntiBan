package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sylvinus/agent-vm/internal/paths"
)

func TestDefault(t *testing.T) {
	elsewhere := t.TempDir()
	t.Setenv("AGENT_VM_STATE_DIR", elsewhere)
	if d, err := Default(); err != nil || string(d) != paths.Host(elsewhere) {
		t.Errorf("override: %q, %v", d, err)
	}
	home := t.TempDir()
	t.Setenv("AGENT_VM_STATE_DIR", "")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if d, err := Default(); err != nil || string(d) != paths.Host(filepath.Join(home, ".agent-vm")) {
		t.Errorf("default: %q, %v", d, err)
	}
}

// A relative folder is made absolute where it is read: the checks that keep
// shares away from the state compare real locations.
func TestDefaultRelative(t *testing.T) {
	wd := t.TempDir()
	t.Chdir(wd)
	t.Setenv("AGENT_VM_STATE_DIR", "rel/state")
	d, err := Default()
	if err != nil || !filepath.IsAbs(string(d)) || filepath.Base(string(d)) != "state" {
		t.Fatalf("relative: %q, %v", d, err)
	}
	real, _ := filepath.EvalSymlinks(wd)
	got, _ := filepath.EvalSymlinks(filepath.Dir(filepath.Dir(string(d))))
	if got != real {
		t.Errorf("relative to %q, want %q", got, real)
	}
}

func TestReadWrite(t *testing.T) {
	d := Dir(filepath.Join(t.TempDir(), "state"))
	if d.Read("x") != "" || d.ReadMarker(VersionOf, "vm") != "" {
		t.Error("read of nothing")
	}
	if err := d.Write("x", []byte("one\n")); err != nil {
		t.Fatal(err)
	}
	if err := d.Write("x", []byte("two\n")); err != nil {
		t.Fatal(err)
	}
	if d.Read("x") != "two" {
		t.Errorf("Read = %q", d.Read("x"))
	}
	if err := d.Write(VersionOf+"vm", []byte("5\n")); err != nil || d.ReadMarker(VersionOf, "vm") != "5" {
		t.Errorf("marker: %q, %v", d.ReadMarker(VersionOf, "vm"), err)
	}
	ents, _ := os.ReadDir(string(d))
	for _, e := range ents {
		if e.Name() != "x" && e.Name() != VersionOf+"vm" {
			t.Errorf("left behind: %s", e.Name())
		}
	}
	// The folder is not there and cannot be made: an error, not a silent skip.
	blocker := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocker, nil, 0o644)
	if err := Dir(filepath.Join(blocker, "sub")).Write("x", nil); err == nil {
		t.Error("written under a file")
	}
}

// Every marker follows its VM, the policy one included.
func TestRenameForget(t *testing.T) {
	d := Dir(t.TempDir())
	for _, kind := range markers {
		d.Write(kind+"a", []byte("1\n"))
	}
	if err := d.Rename("a", "b"); err != nil {
		t.Fatal(err)
	}
	for _, kind := range markers {
		if d.ReadMarker(kind, "b") != "1" || d.ReadMarker(kind, "a") != "" {
			t.Errorf("%s did not follow", kind)
		}
	}
	if err := d.Forget("b", "base"); err != nil {
		t.Fatal(err)
	}
	for _, kind := range markers {
		if _, err := os.Stat(d.Marker(kind, "b")); err == nil {
			t.Errorf("%s left", kind)
		}
	}
}
