//go:build linux || darwin || windows

package reversesshfs

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A removal the server made for the guest is not relayed: the host's event for it must not
// leave a token that swallows the guest's next removal of a file it created again.
func TestRootedOwnRemovalNotRelayed(t *testing.T) {
	c, root, h := setupRootedHandlers(t, false)
	p := func(s string) string { return filepath.Join(root, s) }
	create := func(s string) {
		t.Helper()
		f, err := c.Create(p(s))
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	create("f")
	if err := c.Remove(p("f")); err != nil {
		t.Fatal(err)
	}
	if h.expectRemove(p("f")) {
		t.Fatal("the host's event for the guest's own removal was relayed")
	}
	create("f")
	if err := c.Remove(p("f")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(p("f")); err == nil {
		t.Fatal("rm f; touch f; rm f left the file on the host")
	}

	// A rename away is a removal as well: log rotation.
	create("log")
	if err := c.PosixRename(p("log"), p("log.1")); err != nil {
		t.Fatal(err)
	}
	create("log")
	if h.expectRemove(p("log")) {
		t.Fatal("the host's event for the guest's rename was relayed")
	}
	if err := c.Remove(p("log")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(p("log")); err == nil {
		t.Fatal("the new log was not removed")
	}

	// Once the event window is over, a host deletion is relayed again.
	now := time.Now()
	h.now = func() time.Time { return now.Add(ownRemovalTTL + time.Second) }
	if !h.expectRemove(p("log")) {
		t.Error("not relayed after the window")
	}
}

// A removal that fails removed nothing: the host's deletion of the path that follows is still relayed.
func TestRootedFailedRemovalNotOwn(t *testing.T) {
	c, root, h := setupRootedHandlers(t, false)
	p := func(s string) string { return filepath.Join(root, s) }
	if err := os.MkdirAll(p("d/sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveDirectory(p("d")); err == nil {
		t.Fatal("a folder with something in it was removed")
	}
	if !h.expectRemove(p("d")) {
		t.Error("a removal that failed hid the host's deletion")
	}
}

// A file the guest creates after the host deleted it, within the token's life, is its own.
func TestRootedTokenDroppedByCreate(t *testing.T) {
	c, root, h := setupRootedHandlers(t, false)
	p := func(s string) string { return filepath.Join(root, s) }
	if !h.expectRemove(p("new")) {
		t.Fatal("not expected")
	}
	f, err := c.Create(p("new"))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := c.Remove(p("new")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(p("new")); err == nil {
		t.Fatal("the removal was swallowed though the guest created the file")
	}

	// Same for the other ways to create a name.
	for name, create := range map[string]func(string) error{
		"mkdir":   func(s string) error { return c.Mkdir(s) },
		"symlink": func(s string) error { return c.Symlink("x", s) },
		"rename":  func(s string) error { return c.PosixRename(p("src/main.go"), s) },
	} {
		target := p("t-" + name)
		h.expectRemove(target)
		if err := create(target); err != nil {
			t.Fatal(name, err)
		}
		if err := c.Remove(target); err != nil {
			if err := c.RemoveDirectory(target); err != nil {
				t.Fatal(name, err)
			}
		}
		if _, err := os.Lstat(target); err == nil {
			t.Errorf("%s: the removal was swallowed", name)
		}
	}
}

// A host rm -rf of many files must not stall the server.
func TestExpectRemoveMany(t *testing.T) {
	_, root, h := setupRootedHandlers(t, false)
	start := time.Now()
	const n = 200000
	for i := 0; i < n; i++ {
		h.expectRemove(filepath.Join(root, "src", fmt.Sprint(i)))
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("%d tokens took %v", n, d)
	}
	// They expire in order, and the tables empty out.
	now := time.Now()
	h.now = func() time.Time { return now.Add(noopRemovalTTL + time.Second) }
	h.expectRemove(filepath.Join(root, "x"))
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.relay.tokens.m) != 1 {
		t.Errorf("%d tokens left", len(h.relay.tokens.m))
	}
}

func TestExpiringKeyAddedAgain(t *testing.T) {
	e := newExpiring(10 * time.Second)
	t0 := time.Now()
	e.add("k", t0)
	e.add("k", t0.Add(8*time.Second))
	// The first entry expired, the key is still there with the later expiry.
	if !e.has("k", t0.Add(12*time.Second)) {
		t.Error("key added again was purged by its first entry")
	}
	if e.has("k", t0.Add(19*time.Second)) {
		t.Error("key not expired")
	}
	if len(e.m) != 0 {
		t.Error("left over")
	}
}
