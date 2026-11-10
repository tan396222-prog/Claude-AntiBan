package workbench

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"github.com/sylvinus/agent-vm/internal/guestos"
)

func TestWindowsProjectTransportIsLiveAndConfined(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	folder := t.TempDir()
	if err := os.WriteFile(filepath.Join(folder, "hello.txt"), []byte("live"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(folder, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, ".git", "config"), []byte("protected"), 0600); err != nil {
		t.Fatal(err)
	}
	receipt := filepath.Join(t.TempDir(), "complete")
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWindowsProjectRelayHelper$")
	cmd.Env = append(os.Environ(), "CAGE_WINDOWS_PROJECT_RPC_TEST="+receipt)
	binding := windowsProjectBinding{ID: strings.Repeat("a", 16), Grant: strings.Repeat("b", 64)}
	share, err := startWindowsProjectTransport(cmd, folder, binding)
	if err != nil {
		t.Fatal(err)
	}
	defer share.close()
	select {
	case <-share.done:
	case <-ctx.Done():
		t.Fatal("project transport did not finish")
	}
	if data, err := os.ReadFile(receipt); err != nil || string(data) != "ok" {
		t.Fatalf("SFTP guest assertions failed: %q %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(folder, "saved.txt")); err != nil || string(data) != "atomic" {
		t.Fatalf("atomic host save failed: %q %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(folder, ".git", "config")); err != nil || string(data) != "git-write" {
		t.Fatalf("Git metadata write did not reach host: %q %v", data, err)
	}
}
func TestWindowsProjectRelayHelper(t *testing.T) {
	receipt := os.Getenv("CAGE_WINDOWS_PROJECT_RPC_TEST")
	if receipt == "" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		os.Exit(2)
	}
	var request struct {
		Action string `json:"action"`
		ID     string `json:"id"`
		Grant  string `json:"grant"`
	}
	if json.Unmarshal(line, &request) != nil || request.Action != "mount" || request.ID != strings.Repeat("a", 16) || request.Grant != strings.Repeat("b", 64) {
		os.Exit(2)
	}
	if err = json.NewEncoder(os.Stdout).Encode(map[string]any{"started": true, "id": request.ID, "path": `C:\Users\Guest\Cage\Projects\` + request.ID}); err != nil {
		os.Exit(2)
	}
	client, err := sftp.NewClientPipe(reader, os.Stdout)
	if err != nil {
		os.Exit(2)
	}
	f, err := client.Open("/hello.txt")
	if err != nil {
		os.Exit(2)
	}
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil || string(data) != "live" {
		os.Exit(2)
	}
	root, err := client.RealPath(".")
	if err != nil || root != "/" {
		os.Exit(2)
	}
	if err = exerciseProjectGitWrites(client, ""); err != nil {
		os.Exit(2)
	}
	if f, err = client.Open("/../../host-private"); err == nil {
		f.Close()
		os.Exit(2)
	}
	f, err = client.Create("/atomic.tmp")
	if err != nil {
		os.Exit(2)
	}
	if _, err = f.Write([]byte("atomic")); err != nil {
		os.Exit(2)
	}
	f.Close()
	if err = client.PosixRename("/atomic.tmp", "/saved.txt"); err != nil {
		os.Exit(2)
	}
	if err = os.WriteFile(receipt, []byte("ok"), 0600); err != nil {
		os.Exit(2)
	}
	client.Close()
	os.Exit(0)
}

func TestWindowsDefaultEnvironmentRequiresLiveShare(t *testing.T) {
	s := testServer(t)
	s.windowsGuest = &guestos.WindowsProfile{}
	if err := os.MkdirAll(s.environmentDir(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.ensureProjectShare(s.environmentDir()); err == nil {
		t.Fatal("default host environment became guest home without sharing")
	}
	binding := &windowsProjectBinding{ID: strings.Repeat("a", 16), Grant: strings.Repeat("b", 64), Path: `C:\Users\Guest\Cage\Projects\` + strings.Repeat("a", 16)}
	closed := make(chan struct{})
	share := &projectShare{done: closed, windowsProject: binding}
	s.projectShares = map[string]*projectShare{s.environmentDir(): share}
	if actual, err := s.windowsProjectForHost(s.environmentDir()); err != nil || actual == nil || actual.ID != binding.ID {
		t.Fatalf("default share mapping missing: %v %v", actual, err)
	}
	s.windowsProjectHistory = map[string]string{binding.Path: s.environmentDir()}
	body, _ := json.Marshal(map[string]any{"tabs": []any{map[string]any{"kind": "editor", "workspace": binding.Path}}, "downloads": []any{}})
	mapped, err := s.browserStateForHost(body)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Tabs []struct {
			Workspace string `json:"workspace"`
		} `json:"tabs"`
	}
	if err = json.Unmarshal(mapped, &state); err != nil || len(state.Tabs) != 1 || state.Tabs[0].Workspace != s.environmentDir() {
		t.Fatalf("editor did not map to host share: %s %v", mapped, err)
	}
	close(closed)
	if _, err := s.windowsProjectForHost(s.environmentDir()); err == nil {
		t.Fatal("revoked default grant still usable")
	}
}

func TestWindowsProjectReconnectKeepsWorkspaceAndChangesGrant(t *testing.T) {
	profile, project := t.TempDir(), t.TempDir()
	first, second := windowsProjectIdentity(profile, project), windowsProjectIdentity(profile, project)
	if first.ID != second.ID || len(first.ID) != 16 || first.Grant == second.Grant || len(first.Grant) != 64 {
		t.Fatalf("reconnect changed project identity or reused grant: %#v %#v", first, second)
	}
	if other := windowsProjectIdentity(t.TempDir(), project); other.ID == first.ID {
		t.Fatal("different profile reused project identity")
	}
	if other := windowsProjectIdentity(profile, t.TempDir()); other.ID == first.ID {
		t.Fatal("different project reused project identity")
	}
}
