//go:build !windows

package workbench

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"al.essio.dev/pkg/shellescape"
)

func TestProjectRemovalRefreshesExternalSessions(t *testing.T) {
	s, id, project := metadataFixture(t)
	s.terminals.sessions = nil
	home, _ := filepath.EvalSymlinks(t.TempDir())
	bin := filepath.Join(home, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	const cli = "#!/bin/sh\n[ \"$#\" -eq 2 ] && [ \"$1\" = agents ] && [ \"$2\" = --json ] || exit 2\ncat \"$HOME/agents.json\"\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(cli), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".bash_profile"), []byte("export PATH="+shellescape.Quote(bin)+":/usr/bin:/bin\n"), 0600); err != nil {
		t.Fatal(err)
	}
	mobileTestSSH(t, s, home, []string{"HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin", "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude")})
	record, err := s.mobileCLI.record(s.profile, id)
	if err != nil {
		t.Fatal(err)
	}
	history := filepath.Join(home, ".claude", "projects", "fixture", record.CLIID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(history), 0700); err != nil {
		t.Fatal(err)
	}
	row, _ := json.Marshal(map[string]any{"cwd": guestPath(project), "type": "user", "message": map[string]string{"content": "keep history"}})
	if err := os.WriteFile(history, append(row, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	registry := filepath.Join(home, "agents.json")
	if err := writePrivate(registry, []map[string]any{{"sessionId": record.CLIID, "pid": os.Getpid()}}); err != nil {
		t.Fatal(err)
	}
	s.ready = true
	s.browserControl = browserClientForTest(t, s, browserTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"tabs":[]}`))}, nil
	}))
	removeProjectRequest(t, s, project, 409)
	if !s.mobileCLI.running[id] {
		t.Fatal("removal did not refresh the previously empty process cache")
	}
	if err := os.WriteFile(registry, []byte("invalid JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	removeProjectRequest(t, s, project, 409)
	if err := writePrivate(registry, []any{}); err != nil {
		t.Fatal(err)
	}
	removeProjectRequest(t, s, project, 200)
	paths, err := s.projectPaths()
	if err != nil || slices.Contains(paths, project) {
		t.Fatal("finished session blocked removal", err)
	}
	if _, err := os.Stat(history); err != nil {
		t.Fatal("guest history changed", err)
	}
}
