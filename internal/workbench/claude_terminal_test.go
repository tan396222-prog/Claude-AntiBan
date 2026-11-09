package workbench

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sylvinus/agent-vm/internal/cage"
)

func TestClaudeTerminalRunsSavedCommandOnceAndReturnsToShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("guest Bash integration runs on Unix")
	}
	dir := filepath.Join(t.TempDir(), "project with ' quotes")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	command := `printf '%s\n' 'argument with spaces; $(not-executed)' >> command-result`
	cmd := exec.Command("bash", "-c", terminalScript(dir, "claude", command))
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	cmd.Stdin = strings.NewReader("printf shell-returned > shell-result\nexit\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	b, err := os.ReadFile(filepath.Join(dir, "command-result"))
	if err != nil || string(b) != "argument with spaces; $(not-executed)\n" {
		t.Fatalf("startup command quoting/repetition: %q, %v", b, err)
	}
	b, err = os.ReadFile(filepath.Join(dir, "shell-result"))
	if err != nil || string(b) != "shell-returned" {
		t.Fatalf("terminal did not return to shell: %q, %v", b, err)
	}
}

func TestClaudeTerminalPreferenceValidationAndPersistence(t *testing.T) {
	s := testServer(t)
	cfg, _ := cage.Load(s.configPath)
	if settingsView(cfg, s.preferences).ClaudeTerminalCommand != "claude" {
		t.Fatal("missing default")
	}
	input := settingsView(cfg, s.preferences)
	input.ClaudeTerminalCommand = "claude --continue --model sonnet"
	_, p, err := proposedSettings(input, cfg, s.profile)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.apply(savedProfile{cfg, p}, true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(s.profile, "workbench.json"))
	if err != nil || !strings.Contains(string(data), input.ClaudeTerminalCommand) {
		t.Fatalf("command not saved: %v", err)
	}
	after, _ := cage.Load(s.configPath)
	if after != cfg {
		t.Fatal("terminal preference modified environment configuration")
	}
	for _, bad := range []string{"claude\nexit", "claude\r", "claude\x00", strings.Repeat("x", 4097)} {
		input.ClaudeTerminalCommand = bad
		if _, _, err := proposedSettings(input, cfg, s.profile); err == nil {
			t.Fatal("invalid command accepted")
		}
	}
}

func TestClaudeLoginOutputNeverEntersReconnectHistory(t *testing.T) {
	var manager terminalManager
	t.Cleanup(manager.close)
	selection := claudeSelection{ID: "aaaaaaaaaaaaaaaaaaaaaaaa", GuestID: "aaaaaaaaaaaaaaaaaaaaaaaa"}
	session, err := manager.startSession(terminalChildCommand("echo"), "/project", "claude-login", selection, nil)
	if err != nil {
		t.Fatal(err)
	}
	view := session.subscribe()
	defer view.Close()
	if err = session.enqueueInput(context.Background(), terminalInputLine("private-login-code")); err != nil {
		t.Fatal(err)
	}
	readTerminalUntil(t, view, "child:private-login-code")
	observation := session.subscribe()
	defer observation.Close()
	if strings.Contains(string(observation.History), "private-login-code") || len(session.history) != 0 {
		t.Fatal("login output was retained for reconnect")
	}
	if session.summary()["claude_account_id"] != selection.ID || session.summary()["claude_account_source"] != "managed" {
		t.Fatal("terminal lost its original account binding")
	}
	if again, err := manager.existingClaudeLogin(session.claudeAccountID, true); err != nil || again != session {
		t.Fatal("repeated login did not reattach to the same PTY")
	}
	if _, err := manager.existingClaudeLogin(session.claudeAccountID, false); err == nil {
		t.Fatal("started a Claude terminal while its profile is changing identity")
	}
}

func TestManagedTerminalPreservesTheSelectedProfileInTheGuestShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("guest Bash integration runs on Unix")
	}
	home := t.TempDir()
	const managed = "111111111111111111111111"
	for _, tc := range []struct{ guestID, want string }{
		{"default", home + "/custom-config"},
		{managed, home + "/.local/share/cage/claude-accounts/" + managed + "/config"},
	} {
		work := t.TempDir()
		cmd := exec.Command("bash", "-c", managedClaudeTerminalScript(work, `printf '%s' "$CLAUDE_CONFIG_DIR/$CAGE_CLAUDE_ACCOUNT_ID/$CAGE_CLAUDE_SUBJECT" > observed`, tc.guestID, "expected", false))
		cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "CLAUDE_CONFIG_DIR=" + home + "/custom-config"}
		cmd.Stdin = strings.NewReader("exit\n")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("guest launch: %v: %s", err, b)
		}
		b, err := os.ReadFile(filepath.Join(work, "observed"))
		if err != nil || string(b) != tc.want+"/"+tc.guestID+"/expected" {
			t.Fatalf("profile override or subject lost: %s, %v", b, err)
		}
	}
}

func TestClaudeTerminalSourceSurvivesAdoptingAnIndependentProfile(t *testing.T) {
	var manager terminalManager
	t.Cleanup(manager.close)
	account := claudeSelection{ID: "aaaaaaaaaaaaaaaaaaaaaaaa", GuestID: "default"}
	original, err := manager.startSession(terminalChildCommand("echo"), "/project", "claude", account, nil)
	if err != nil {
		t.Fatal(err)
	}
	account.GuestID = "bbbbbbbbbbbbbbbbbbbbbbbb"
	adopted, err := manager.startSession(terminalChildCommand("echo"), "/project", "claude", account, nil)
	if err != nil {
		t.Fatal(err)
	}
	if original.summary()["claude_account_source"] != "default" || adopted.summary()["claude_account_source"] != "managed" ||
		original.summary()["claude_account_id"] != adopted.summary()["claude_account_id"] {
		t.Fatal("adoption changed an existing terminal's account source")
	}
}

func TestDefaultClaudeProbeUsesRegularLoginEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("guest Bash integration runs on Unix")
	}
	home := t.TempDir()
	profile := "echo startup-banner\nexport CAGE_FIXTURE_AUTH=shell-route\n"
	if err := os.WriteFile(filepath.Join(home, ".bash_profile"), []byte(profile), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "show-environment"), []byte("#!/bin/sh\nprintf '%s' \"$CAGE_FIXTURE_AUTH|$PWD|$1|$2\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	script := strings.Replace(claudeSnapshotCommand("default"), "/usr/local/bin/cage-claude-account", `"$HOME/show-environment"`, 1)
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = t.TempDir()
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	b, err := cmd.Output()
	realHome, _ := filepath.EvalSymlinks(home)
	if err != nil || (string(b) != "shell-route|"+home+"|snapshot|default" && string(b) != "shell-route|"+realHome+"|snapshot|default") {
		t.Fatalf("login env or guest JSON stream changed: %q, %v", b, err)
	}
}
