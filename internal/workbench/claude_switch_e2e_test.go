//go:build !windows

package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/google/uuid"
)

func TestClaudeAccountSwitchThroughSharedSession(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		name := "manual"
		if automatic {
			name = "automatic"
		}
		t.Run(name, func(t *testing.T) { testClaudeAccountSwitchThroughSharedSession(t, automatic) })
	}
}

func testClaudeAccountSwitchThroughSharedSession(t *testing.T, automatic bool) {
	binary := os.Getenv("CAGE_TEST_CLAUDE_CLI")
	if runtime.GOOS != "darwin" || !filepath.IsAbs(binary) {
		t.Skip("requires official CLI and macOS sandbox")
	}
	binary, _ = filepath.EvalSymlinks(binary)
	s := testServer(t)
	home, _ := filepath.EvalSymlinks(t.TempDir())
	project, _ := filepath.EvalSymlinks(t.TempDir())
	s.ready = true
	s.preferences.Workspace = project
	if err := writePrivate(filepath.Join(s.profile, "environment.json"), map[string]string{"directory": project}); err != nil {
		t.Fatal(err)
	}
	personal, _ := os.UserHomeDir()
	quote := func(value string) string { b, _ := json.Marshal(value); return string(b) }
	policy := "(version 1)(allow default)(deny mach-lookup)(deny file-read-data (subpath " + quote(personal) + ") (subpath \"/Library/Keychains\"))(allow file-read* (literal " + quote(binary) + "))(deny network-outbound)(allow network-outbound (remote ip \"localhost:*\"))"
	bin := filepath.Join(home, "bin")
	_ = os.Mkdir(bin, 0700)
	shim := "#!/bin/sh\nexec /usr/bin/sandbox-exec -p " + shellescape.Quote(policy) + " " + shellescape.Quote(binary) + " --model sonnet --permission-mode manual \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(shim), 0700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(home, ".bash_profile"), []byte("export PATH="+shellescape.Quote(bin)+":/usr/bin:/bin\n"), 0600)
	helper := filepath.Join(bin, "cage-claude-account")
	original, _ := os.ReadFile("../cage/claude-account.py")
	if err := os.WriteFile(helper, original, 0700); err != nil {
		t.Fatal(err)
	}
	var resumed atomic.Bool
	model := mobileTestModel(t, &resumed)
	t.Cleanup(model.Close)
	var sharedContext atomic.Bool
	observed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/messages" {
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			if strings.Contains(string(body), "follow-up") {
				sharedContext.Store(strings.Contains(string(body), "GLOBAL_SHARED_INSTRUCTION") && strings.Contains(string(body), "PROJECT_SHARED_INSTRUCTION") && strings.Contains(string(body), "shared-cage-test-skill") && strings.Contains(string(body), "PROJECT_SHARED_MEMORY"))
			}
		}
		model.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(observed.Close)
	proxy, ca := mobileOfficialTestProxy(t, observed)
	if err := os.MkdirAll(filepath.Join(home, ".claude/skills/shared-cage-test-skill"), 0700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(home, ".claude/skills/shared-cage-test-skill/SKILL.md"), []byte("---\nname: shared-cage-test-skill\ndescription: Fixture shared skill\n---\nUse for the fixture.\n"), 0600)
	_ = os.WriteFile(filepath.Join(home, ".claude/CLAUDE.md"), []byte("GLOBAL_SHARED_INSTRUCTION"), 0600)
	_ = os.WriteFile(filepath.Join(project, "CLAUDE.md"), []byte("PROJECT_SHARED_INSTRUCTION"), 0600)
	memory := filepath.Join(home, ".claude/projects", regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(project, "-"), "memory")
	_ = os.MkdirAll(memory, 0700)
	_ = os.WriteFile(filepath.Join(memory, "MEMORY.md"), []byte("PROJECT_SHARED_MEMORY"), 0600)
	list := claudeAccountList{Scheduling: claudeScheduling{Mode: "sequential", SwitchPolicy: "ask"}}
	features := map[string]bool{}
	if data, err := os.ReadFile(os.Getenv("CAGE_TEST_CHANNEL_FEATURES")); err == nil {
		_ = json.Unmarshal(data, &features)
	}
	if err := writePrivate(filepath.Join(home, ".claude.json"), map[string]any{"hasCompletedOnboarding": true, "cachedGrowthBookFeatures": features, "cachedGrowthBookFeaturesAt": time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{strings.Repeat("a", 24), strings.Repeat("b", 24)} {
		email := id[:1] + "@example.invalid"
		list.Accounts = append(list.Accounts, claudeAccount{ID: id, Label: id[:1], Email: email, Enabled: true, Status: "ready", HandoffGroup: "test"})
		config := filepath.Join(home, ".local/share/cage/claude-accounts", id, "config")
		if err := writePrivate(filepath.Join(config, ".claude.json"), map[string]any{"hasCompletedOnboarding": true, "cachedGrowthBookFeatures": features, "cachedGrowthBookFeaturesAt": time.Now().UnixMilli(), "oauthAccount": map[string]string{"accountUuid": uuid.NewString(), "emailAddress": email}, "projects": map[string]any{project: map[string]bool{"hasTrustDialogAccepted": true}}}); err != nil {
			t.Fatal(err)
		}
		if err := writePrivate(filepath.Join(config, ".credentials.json"), map[string]any{"claudeAiOauth": map[string]any{"accessToken": "sk-ant-oat01-synthetic-" + id[:1], "refreshToken": "synthetic", "expiresAt": time.Now().Add(time.Hour).UnixMilli(), "scopes": []string{"user:inference", "user:profile"}, "subscriptionType": "pro"}}); err != nil {
			t.Fatal(err)
		}
		if err := writePrivate(filepath.Join(config, "settings.json"), map[string]any{"skipDangerousModePermissionPrompt": true, "statusLine": map[string]string{"type": "command", "command": "printf test"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.saveClaudeAccounts(list); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin", "HTTP_PROXY=" + proxy, "HTTPS_PROXY=" + proxy, "NO_PROXY=localhost,127.0.0.1", "NODE_EXTRA_CA_CERTS=" + ca, "SSL_CERT_FILE=" + ca, "DISABLE_AUTOUPDATER=1", "TERM=xterm-256color"}
	mobileTestSSHRewrite(t, s, home, env, func(command string) string {
		return strings.ReplaceAll(command, "/usr/local/bin/cage-claude-account", helper)
	})
	// Rewrite the helper path in decoded launch payloads at the test boundary.
	s.claudeAccounts.probe = func(ctx context.Context, id string) (claudeGuestSnapshot, error) {
		return claudeGuestSnapshot{LoggedIn: true, Email: id[:1] + "@example.invalid"}, nil
	}
	t.Cleanup(func() {
		if t.Failed() {
			states, _ := filepath.Glob(filepath.Join(home, ".local/share/cage/cli-channels/*/state.json"))
			for _, path := range states {
				b, _ := os.ReadFile(path)
				t.Log(string(b))
			}
		}
		output, _ := exec.Command("pgrep", "-f", regexp.QuoteMeta(home)).Output()
		for _, value := range strings.Fields(string(output)) {
			pid, err := strconv.Atoi(value)
			if err == nil && pid > 0 {
				if t.Failed() {
					out, _ := exec.Command("ps", "-o", "pid,ppid,stat,comm", "-p", value).Output()
					t.Log(string(out))
				}
				p, _ := os.FindProcess(pid)
				_ = p.Kill()
				_ = p.Release()
			}
		}
	})
	t.Cleanup(s.terminals.closeAll)
	id := uuid.NewString()
	call := func(method, path string, body any) mobileSession {
		t.Helper()
		raw, _ := json.Marshal(body)
		if body == nil {
			raw = nil
		}
		response := request(s, method, "/api/mobile/cli/sessions/"+id+path, s.origin, string(raw), true)
		if response.Code != 200 {
			t.Fatalf("%s %s: %d %s", method, path, response.Code, response.Body.String())
		}
		var view mobileSession
		if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
		return view
	}
	response := request(s, "POST", "/api/mobile/cli/sessions", s.origin, `{"id":"`+id+`","workspace":"`+project+`"}`, true)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	wait := func(predicate func(mobileSession) bool) mobileSession {
		t.Helper()
		deadline := time.Now().Add(35 * time.Second)
		var view mobileSession
		for time.Now().Before(deadline) {
			view = call("GET", "", nil)
			if predicate(view) {
				return view
			}
			time.Sleep(100 * time.Millisecond)
		}
		for _, terminal := range s.terminals.sessions {
			terminal.mu.Lock()
			t.Log(string(terminal.history))
			terminal.mu.Unlock()
		}
		t.Fatalf("session did not reach expected state: %#v", view)
		return view
	}
	view := wait(func(v mobileSession) bool { return v.CanSend != nil && *v.CanSend })
	if view.AccountID != list.Accounts[0].ID {
		t.Fatal("source account was not selected", view.AccountID)
	}
	messageID := uuid.NewString()
	call("POST", "/messages", map[string]string{"request_id": messageID, "text": "initial mobile prompt"})
	view = wait(func(v mobileSession) bool { return len(v.Turns) > 0 && v.Turns[0].Approval != nil })
	call("POST", "/approval", map[string]any{"request_id": messageID, "approval_id": view.Turns[0].Approval.ID, "allow": true})
	view = wait(func(v mobileSession) bool {
		return len(v.Turns) > 0 && v.Turns[0].Text == "isolated tool completed" && v.Turns[0].Status == "completed"
	})
	before, err := os.ReadFile(filepath.Join(project, "mobile-approved.txt"))
	if err != nil {
		t.Fatal(err)
	}
	m := &s.mobileCLI
	m.mu.Lock()
	record, _ := m.record(s.profile, id)
	sourceID := record.CLIID
	m.mu.Unlock()
	switchID := uuid.NewString()
	input := claudeSwitchInput{Action: "switch", RequestID: switchID, SourceID: sourceID, Target: list.Accounts[1].ID, Confirm: true}
	raw, _ := json.Marshal(input)
	if automatic {
		// The real channel's StopFailure observation and a known exhausted
		// quota are both required before the same production handoff can run.
		record.SwitchPolicy = "allow"
		if err := writePrivate(mobileRecordPath(s.profile, id), record); err != nil {
			t.Fatal(err)
		}
		statePath := filepath.Join(home, ".local/share/cage/cli-channels", record.Connection, "state.json")
		data, err := os.ReadFile(statePath)
		if err != nil {
			t.Fatal(err)
		}
		var state map[string]any
		if err := json.Unmarshal(data, &state); err != nil {
			t.Fatal(err)
		}
		state["account_failure"] = "rate_limit"
		if err := writePrivate(statePath, state); err != nil {
			t.Fatal(err)
		}
		list.Accounts[0].Quota = &claudeQuota{FiveHour: &claudeWindow{UsedPercentage: 100, ResetsAt: time.Now().Add(time.Hour).Unix(), ObservedAt: time.Now().Unix()}}
		if err := s.saveClaudeAccounts(list); err != nil {
			t.Fatal(err)
		}
		epoch, _ := s.account.access()
		ctx := context.WithValue(context.Background(), mobileEpochKey{}, epoch)
		lastUsage := time.Now()
		s.maybeSwitchClaudeAccount(ctx, id, &lastUsage)
		m.mu.Lock()
		latest, err := m.record(s.profile, id)
		m.mu.Unlock()
		if err != nil || latest.Switch == nil {
			t.Fatalf("automatic handoff did not start: %v", err)
		}
		input.RequestID = latest.Switch.ID
		raw, _ = json.Marshal(input)
	}
	response = request(s, "POST", "/api/cli/sessions/"+id+"/account", s.origin, string(raw), true)
	if response.Code == 409 && strings.Contains(response.Body.String(), "原进程尚未退出") {
		// Shutdown can wait for a last hook. Reuse the same durable operation;
		// the retry must never create a second source or target execution.
		response = request(s, "POST", "/api/cli/sessions/"+id+"/account", s.origin, string(raw), true)
	}
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	view = wait(func(v mobileSession) bool {
		return v.CanSend != nil && *v.CanSend && v.AccountSwitch != nil && v.AccountSwitch.Phase == "completed"
	})
	if len(view.Turns) != 1 || view.AccountID != list.Accounts[1].ID {
		t.Fatalf("history or binding changed incorrectly: %#v", view)
	}
	// Replay an acknowledged pre-switch request through the new account channel.
	call("POST", "/messages", map[string]string{"request_id": messageID, "text": "initial mobile prompt"})
	call("POST", "/messages", map[string]string{"request_id": uuid.NewString(), "text": "follow-up"})
	view = wait(func(v mobileSession) bool { return len(v.Turns) == 2 && v.Turns[1].Text == "isolated continuation" })
	after, _ := os.ReadFile(filepath.Join(project, "mobile-approved.txt"))
	if string(before) != string(after) || !resumed.Load() || !sharedContext.Load() {
		t.Fatal("completed work lost or history not resumed")
	}
	response = request(s, "POST", "/api/cli/sessions/"+id+"/account", s.origin, string(raw), true)
	if response.Code != 200 || len(s.terminals.snapshot()) != 1 {
		t.Fatal("replayed switch created another execution", response.Code)
	}
	t.Log("shared session switched A to B through authenticated desktop/mobile routes, retained tool history, and deduplicated original message and switch")
}
