//go:build e2e && darwin && arm64

package workbench

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sylvinus/agent-vm/internal/guestos"
)

// Uses an existing test guest, a separate host account/session catalog and a
// new random guest conversation. No subscription credentials are required.
func TestMacOSGuestClaudeCode(t *testing.T) {
	profile := os.Getenv("CAGE_TEST_MACOS_CLI_PROFILE")
	if profile == "" {
		t.Skip("set CAGE_TEST_MACOS_CLI_PROFILE to an explicitly selected test guest")
	}
	binding, err := guestos.LoadMacOSProfile(profile)
	if err != nil || binding == nil {
		t.Fatal("test guest binding unavailable")
	}
	s := testServer(t)
	s.macosGuest = binding
	for _, file := range []string{"macos-known-hosts", "state/lima/_config/user", "state/lima/mac/ssh.config", "state/lima/mac/cage-running.json"} {
		dest := filepath.Join(s.profile, file)
		if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(profile, file), dest); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := writePrivate(filepath.Join(s.profile, "environment.json"), map[string]string{"directory": dir}); err != nil {
		t.Fatal(err)
	}
	s.preferences.Workspace = dir
	s.ready = true
	call := func(method, route, body string, result any) {
		t.Helper()
		req := httptest.NewRequest(method, s.origin+route, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", s.origin)
		req.AddCookie(&http.Cookie{Name: s.cookieName(), Value: s.session})
		req.AddCookie(&http.Cookie{Name: s.cookieName() + "-desktop", Value: trustedViewProof(s.token)})
		resp := httptest.NewRecorder()
		s.handler().ServeHTTP(resp, req)
		if resp.Code != 200 {
			t.Fatalf("%s: %d %s", route, resp.Code, resp.Body.String())
		}
		if err := json.Unmarshal(resp.Body.Bytes(), result); err != nil {
			t.Fatal(err)
		}
	}
	var tab struct {
		ID     string
		Shared string `json:"cli_session_id"`
	}
	call("POST", "/api/terminals", `{"kind":"claude"}`, &tab)
	if tab.ID == "" || !validCLIUUID(tab.Shared) {
		t.Fatal("no shared terminal")
	}
	session := s.terminals.get(tab.ID)
	defer func() {
		s.terminals.close()
		s.activities.Wait()
		s.mobileCLI.mu.Lock()
		rec, err := s.mobileCLI.record(s.profile, tab.Shared)
		s.mobileCLI.mu.Unlock()
		if err != nil || rec.Connection != session.connectionID {
			t.Error("missing QA session binding")
			return
		}
		// Remove only state created by this invocation in the explicitly selected QA guest.
		ctx, cancel := context.WithTimeout(context.Background(), 16*time.Second)
		defer cancel()
		var ended struct{ OK bool }
		for !ended.OK && ctx.Err() == nil {
			err = s.runCLIChannel(ctx, "purge", map[string]string{"cli_id": rec.CLIID, "config": rec.GuestConfig, "workspace": rec.GuestWorkspace, "account": "default"}, &ended)
			if !ended.OK {
				time.Sleep(250 * time.Millisecond)
			}
		}
		if !ended.OK {
			t.Errorf("could not clean up this QA conversation: %v", err)
		}
	}()
	deadline := time.Now().Add(40 * time.Second)
	for {
		session.mu.Lock()
		output := cliStartupText(session.history)
		session.mu.Unlock()
		if strings.Contains(output, "UnabletoconnecttoAnthropic") {
			t.Fatal("the test guest needs a currently authorized network connection")
		}
		if strings.Contains(output, "ClaudeCodev") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("official native CLI did not present its interactive terminal")
		}
		time.Sleep(150 * time.Millisecond)
	}
	var index struct {
		Sessions []mobileSummary
		Notice   string
	}
	call("GET", "/api/cli/sessions", "", &index)
	found := false
	for _, item := range index.Sessions {
		if item.ID == tab.Shared {
			found = true
		}
	}
	if !found || index.Notice != "" {
		t.Fatalf("CLI catalog did not rediscover the active conversation: %s", index.Notice)
	}
	var reopened mobileSession
	call("POST", "/api/cli/sessions/"+tab.Shared+"/open", `{}`, &reopened)
	if reopened.DesktopTerminalID != tab.ID {
		t.Fatalf("reconnect created a different terminal")
	}
	if n := len(s.terminals.snapshot()); n != 1 {
		t.Fatalf("reconnect published %d terminals", n)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	read, err := s.readClaudeGuestSnapshot(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ordinary macOS account launched the official Claude Code terminal; active session rediscovered and reattached; native account probe succeeded (signed in: %t)", read.LoggedIn)
}
