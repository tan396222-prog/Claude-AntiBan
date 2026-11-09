package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sylvinus/agent-vm/internal/cage"
)

func TestWindowsEntryDropsOpenSSHAncestorDescriptors(t *testing.T) {
	names := []string{"OPENSSH_STDIO_MODE", "c28fc6f98a2c44abbbd89d6a3037d0d9_POSIX_FD_STATE"}
	for _, name := range names {
		t.Setenv(name, "ancestor-state")
	}
	// A missing isolated config reaches runtime initialization, then returns
	// before any proxy connection, VM work or access to the user's profile.
	t.Setenv(cage.ConfigEnv, filepath.Join(t.TempDir(), "missing.json"))
	previous := os.Args
	os.Args = []string{"cage", "check"}
	t.Cleanup(func() { os.Args = previous })
	if code := run(); code != 1 {
		t.Fatalf("missing configuration returned %d", code)
	}
	for _, name := range names {
		if _, exists := os.LookupEnv(name); exists {
			t.Errorf("%s survived Windows runtime initialization", name)
		}
	}
}
