package workbench

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A client project has no directory on the cloud host. It must be usable by
// the normal project picker and terminal resolver without ever becoming a
// persisted host-directory grant, even when the user makes it current.
func TestGuestClientMountActivationAndRevocation(t *testing.T) {
	s := testServer(t)
	s.ready = true
	s.preferences.Workspace = t.TempDir()
	prefsFile := filepath.Join(s.profile, "workbench.json")
	if err := writePrivate(prefsFile, s.preferences); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(prefsFile)
	if err != nil {
		t.Fatal(err)
	}
	path := clientMountRoot + "/" + strings.Repeat("b", 32)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mount := &guestClientMount{ctx: ctx}
	s.clientMounts = map[string]*guestClientMount{path: mount}
	if _, err := s.connectProject(path, true); !errors.Is(err, errClientMountGone) {
		t.Fatalf("accepted project before mount verification: %v", err)
	}
	mount.ready = true
	body := `{"workspace":"` + path + `","add":false,"activate":true}`
	if got := request(s, "POST", "/api/projects/open", s.origin, body, true); got.Code != http.StatusOK {
		t.Fatalf("activate: %d %s", got.Code, got.Body)
	}
	cwd, err := s.connectProject("", false)
	if err != nil || cwd != path {
		t.Fatalf("default terminal workspace = %q, %v", cwd, err)
	}
	if !strings.HasPrefix(terminalScript(cwd, "claude", "claude"), "cd "+path+" &&") ||
		!strings.HasPrefix(managedClaudeTerminalScript(cwd, "claude", "default", "sub", false), "cd "+path+" &&") {
		t.Fatal("CLI would launch outside the mounted workspace")
	}
	after, err := os.ReadFile(prefsFile)
	if err != nil || string(after) != string(before) {
		t.Fatal("activating a client mount persisted it as the default")
	}
	if _, err := os.Stat(filepath.Join(s.profile, "projects.json")); !os.IsNotExist(err) {
		t.Fatal("created a persistent grant for the ephemeral guest path")
	}
	cancel()
	delete(s.clientMounts, path)
	for _, name := range []string{"", path, path + "/..", path + "/subdir"} {
		if _, err := s.connectProject(name, true); !errors.Is(err, errClientMountGone) {
			t.Fatalf("admitted revoked/unregistered guest path %q: %v", name, err)
		}
	}
	paths, err := s.projectPaths()
	if err != nil || slices.Contains(paths, path) {
		t.Fatal("revoked share remained in available projects", err)
	}
}

func TestGuestClientShareRequiresWorkbenchSessionOriginAndUnlock(t *testing.T) {
	s := testServer(t)
	url := "/api/projects/client-share?id=" + strings.Repeat("c", 32)
	if got := request(s, "POST", url, s.origin, "", false); got.Code != http.StatusUnauthorized {
		t.Fatalf("missing session: HTTP %d", got.Code)
	}
	if got := request(s, "POST", url, "https://untrusted.example", "", true); got.Code != http.StatusForbidden {
		t.Fatalf("foreign origin: HTTP %d", got.Code)
	}
	s.account.lock()
	if got := request(s, "POST", url, s.origin, "", true); got.Code != http.StatusLocked {
		t.Fatalf("locked account: HTTP %d", got.Code)
	}
}
