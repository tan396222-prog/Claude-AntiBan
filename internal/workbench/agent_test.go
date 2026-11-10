package workbench

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/guestos"
)

func agentTestController(t *testing.T) *Server {
	t.Helper()
	s := testServer(t)
	identity, err := currentController()
	if err != nil {
		t.Fatal(err)
	}
	s.identity = identity
	server := httptest.NewServer(s.handler())
	t.Cleanup(server.Close)
	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())
	s.origin = "http://localhost:" + u.Port()
	lock, err := openControllerLock(s.profile, os.O_CREATE|os.O_RDWR)
	if err != nil {
		t.Fatal(err)
	}
	if err = lockController(lock); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unlockController(lock); lock.Close() })
	if err := writePrivate(filepath.Join(s.profile, "control.json"), controlFile{Port: port, Token: s.token, PID: os.Getpid(), Controller: identity}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAgentAuthenticationAndLockedAccount(t *testing.T) {
	s := agentTestController(t)
	if _, err := AgentCall(context.Background(), s.configPath, "settings.get", nil); err != nil {
		t.Fatal(err)
	}
	s.account.lock()
	_, err := AgentCall(context.Background(), s.configPath, "settings.get", nil)
	var ae *AgentError
	if !errors.As(err, &ae) || ae.Code != "account_locked" {
		t.Fatalf("locked: %v", err)
	}
	if _, err := AgentCall(context.Background(), s.configPath, "account.status", nil); err != nil {
		t.Fatal(err)
	}
	if w := request(s, "POST", "/api/agent/settings", s.origin, `{"patch":{"keyboard":"native"}}`, false); w.Code != 401 {
		t.Fatal("agent routes bypassed session", w.Code)
	}
}

func TestAgentSettingsPatchPreservesOmittedFields(t *testing.T) {
	s := agentTestController(t)
	s.preferences.ClaudeTerminalCommand = "claude --verbose"
	before, err := cage.Load(s.configPath)
	if err != nil {
		t.Fatal(err)
	}
	data, err := AgentCall(context.Background(), s.configPath, "settings.patch", json.RawMessage(`{"patch":{"browser_home_url":"https://example.org/"},"dry_run":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "https://example.org/") || s.preferences.BrowserHomeURL != "" {
		t.Fatalf("dry run: %s", data)
	}
	data, err = AgentCall(context.Background(), s.configPath, "settings.patch", json.RawMessage(`{"patch":{"browser_home_url":"https://example.org/"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "https://example.org/") {
		t.Fatal("homepage was not confirmed", string(data))
	}
	if s.preferences.ClaudeTerminalCommand != "claude --verbose" || s.preferences.BrowserHomeURL != "https://example.org/" {
		t.Fatalf("preferences lost: %+v", s.preferences)
	}
	after, _ := cage.Load(s.configPath)
	if before != after {
		t.Fatal("unrelated network config changed")
	}
	for _, input := range []string{`{"patch":{"proxy_url":"socks5://127.0.0.1:1"}}`, `{"patch":{"keyboard":null}}`, `{"patch":{"keyboard":"invalid"}}`, `{"patch":{}}`} {
		if _, err := AgentCall(context.Background(), s.configPath, "settings.patch", json.RawMessage(input)); err == nil {
			t.Fatal("accepted invalid patch", input)
		}
	}
}

func TestAgentPartialExitPolicyPreservesAnchor(t *testing.T) {
	s := agentTestController(t)
	cfg, err := cage.Load(s.configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ExitPolicy = cage.ExitPolicy{Mode: "dynamic", Scope: "city", IP: "8.8.8.8", CountryCode: "US", Region: "California", City: "Los Angeles"}
	if err := writePrivate(s.configPath, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := AgentCall(context.Background(), s.configPath, "settings.patch", json.RawMessage(`{"patch":{"exit_policy":{"mode":"static"}},"dry_run":true}`))
	if err != nil {
		t.Fatal(err)
	}
	var view Settings
	_ = json.Unmarshal(data, &view)
	if view.ExitPolicy.Mode != "static" || view.ExitPolicy.Scope != "city" || view.ExitPolicy.IP != "8.8.8.8" || view.ExitPolicy.City != "Los Angeles" {
		t.Fatalf("network anchor discarded: %+v", view.ExitPolicy)
	}
	_, err = AgentCall(context.Background(), s.configPath, "settings.patch", json.RawMessage(`{"patch":{"exit_policy":{"ip":null}},"dry_run":true}`))
	if err == nil {
		t.Fatal("null cleared network anchor")
	}
}

func TestAgentJobIdentityAndCancellation(t *testing.T) {
	s := agentTestController(t)
	s.setJobForTest(Job{ID: "012345abcdef", Running: true})
	if _, err := AgentJob(context.Background(), s.configPath, "012345abcdef"); err != nil {
		t.Fatal(err)
	}
	s.setJobForTest(Job{ID: "abcdef012345"})
	_, err := AgentJob(context.Background(), s.configPath, "012345abcdef")
	var ae *AgentError
	if !errors.As(err, &ae) || ae.Code != "job_not_found" {
		t.Fatalf("wrong job accepted: %v", err)
	}
	var manager jobManager
	manager.state = Job{ID: "012345abcdef", Running: true}
	execution := &jobExecution{id: manager.state.ID, ctx: context.Background()}
	manager.finishLocked(execution, context.Canceled)
	if manager.state.Running || !manager.state.Canceled {
		t.Fatalf("cancellation hidden: %+v", manager.state)
	}
}

func TestAgentRedactionPreservesOperationalValues(t *testing.T) {
	input := `{"id":"012345abcdef","clone_key":"` + strings.Repeat("a", 64) + `","workspace":"/Users/test/project","dns_url":"https://1.1.1.1/dns-query","error":"fetch https://secret.example/sub?token=very-secret failed","job":{"log":"password=hidden"}}`
	output := agentRedact(input)
	if !json.Valid([]byte(output)) || strings.Contains(output, "very-secret") || strings.Contains(output, "hidden") {
		t.Fatal(output)
	}
	for _, value := range []string{"012345abcdef", strings.Repeat("a", 64), "/Users/test/project", "https://1.1.1.1/dns-query"} {
		if !strings.Contains(output, value) {
			t.Fatal("operational field redacted", output)
		}
	}
}

func TestAgentNoImplicitStartAndUnknownInput(t *testing.T) {
	profile := t.TempDir()
	path := filepath.Join(profile, "config.json")
	for _, tc := range []struct{ op, input, code string }{{"does.not.exist", "{}", "unknown_operation"}, {"environment.stop", `{"surprise":true}`, "invalid_input"}, {"settings.get", "{}", "service_unavailable"}} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := AgentCall(ctx, path, tc.op, json.RawMessage(tc.input))
		cancel()
		var ae *AgentError
		if !errors.As(err, &ae) || ae.Code != tc.code {
			t.Fatalf("%s: %v", tc.op, err)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("read created profile")
	}
	for _, path := range []string{"/api/agent/settings", "/api/agent/jobs/012345abcdef"} {
		if !macOSWorkbenchRoute(path) || !windowsWorkbenchRoute(path) {
			t.Fatal("missing native platform route", path)
		}
	}
	if !macOSSetupRoute("/api/agent/jobs/012345abcdef") {
		t.Fatal("cannot track first setup")
	}
}

func TestAgentCannotFollowSelectedRemote(t *testing.T) {
	s := agentTestController(t)
	s.remote = newRemoteGateway(s.profile)
	t.Cleanup(func() { s.remote.close() })
	s.remote.setRemote(true)
	_, err := AgentCall(context.Background(), s.configPath, "environment.stop", json.RawMessage(`{}`))
	var ae *AgentError
	if !errors.As(err, &ae) || ae.Code != "state_conflict" {
		t.Fatalf("remote target accepted: %v", err)
	}
	if s.jobs.snapshot().ID != "" {
		t.Fatal("remote rejection fell back to local mutation")
	}
}

func TestAgentRejectsOtherConfigFileInSameProfile(t *testing.T) {
	s := agentTestController(t)
	_, err := AgentCall(context.Background(), filepath.Join(s.profile, "other.json"), "account.status", nil)
	var ae *AgentError
	if !errors.As(err, &ae) || ae.Code != "config_path_mismatch" {
		t.Fatalf("wrong config accepted: %v", err)
	}
	s.clientOnly = true
	_, err = AgentCall(context.Background(), s.configPath, "account.status", nil)
	if !errors.As(err, &ae) || ae.Code != "controller_mismatch" {
		t.Fatalf("remote-only client accepted: %v", err)
	}
}

func TestAgentNativeDiagnosticExport(t *testing.T) {
	for _, system := range []string{"linux", "macos", "windows"} {
		t.Run(system, func(t *testing.T) {
			s := agentTestController(t)
			if system == "macos" {
				s.macosGuest = &guestos.MacOSProfile{Instance: "test", User: "test"}
			}
			if system == "windows" {
				s.windowsGuest = &guestos.WindowsProfile{}
			}
			s.account.lock()
			path := filepath.Join(t.TempDir(), "diagnostics.zip")
			if err := ExportAgentDiagnostics(s.configPath, path); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil || len(before) == 0 {
				t.Fatal("missing bundle", err)
			}
			archive, err := zip.OpenReader(path)
			if err != nil {
				t.Fatal(err)
			}
			defer archive.Close()
			for _, file := range archive.File {
				if file.Name == "manifest.json" {
					reader, err := file.Open()
					if err != nil {
						t.Fatal(err)
					}
					body, _ := io.ReadAll(reader)
					reader.Close()
					if !strings.Contains(string(body), `"guest_collection": "environment unavailable"`) {
						t.Fatal("did not use authenticated native diagnostic endpoint", string(body))
					}
				}
			}
			if err := ExportAgentDiagnostics(s.configPath, path); err == nil {
				t.Fatal("overwrote previous diagnostic")
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("existing bundle changed")
			}
		})
	}
}
