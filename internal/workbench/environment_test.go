package workbench

import (
	"bytes"
	"encoding/json"
	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/guestos"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvironmentUpgradeSeparatesPrivateLegacyWorkspaceFromVMIdentity(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, system := range []string{"linux", "macos", "windows"} {
		t.Run(system, func(t *testing.T) {
			s := testServer(t)
			s.profile = filepath.Join(home, ".claude-cage", system)
			legacy := s.profile + "-workspace"
			selected := filepath.Join(home, "projects", system)
			for _, path := range []string{s.profile, legacy, selected} {
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			s.preferences.Workspace = selected
			if system == "macos" {
				s.macosGuest = &guestos.MacOSProfile{Instance: "mac", User: "guest"}
			} else if system == "windows" {
				s.windowsGuest = &guestos.WindowsProfile{}
			}
			if err := writePrivate(filepath.Join(s.profile, "environment.json"), map[string]string{"directory": legacy}); err != nil {
				t.Fatal(err)
			}
			identity := cage.InstanceName(s.profile, legacy)
			disk := filepath.Join(s.profile, "state", "lima", identity, "disk")
			if err := writePrivate(filepath.Join(filepath.Dir(disk), "lima.yaml"), map[string]string{}); err != nil {
				t.Fatal(err)
			}
			original := []byte("persistent disk and existing project data")
			for _, file := range []string{disk, filepath.Join(legacy, "project.txt"), filepath.Join(s.profile, "account.json")} {
				if err := os.WriteFile(file, original, 0600); err != nil {
					t.Fatal(err)
				}
			}
			for restart := 0; restart < 2; restart++ {
				if err := s.prepareEnvironment(); err != nil {
					t.Fatal(err)
				}
				if s.environmentDir() != selected || s.workspace() != selected || cage.InstanceName(s.profile, selected) != identity {
					t.Fatal("startup changed the VM identity or selected project")
				}
				if _, err := validateWorkspace(legacy, s.profile); err == nil {
					t.Fatal("legacy location bypassed private-profile protection")
				}
				for _, file := range []string{disk, filepath.Join(legacy, "project.txt"), filepath.Join(s.profile, "account.json")} {
					if data, err := os.ReadFile(file); err != nil || !bytes.Equal(data, original) {
						t.Fatalf("startup changed existing personal data: %s %v", file, err)
					}
				}
			}
		})
	}
}

type browserTransport func(*http.Request) (*http.Response, error)

func (f browserTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEnvironmentIdentityDoesNotFollowLastProject(t *testing.T) {
	s := testServer(t)
	s.profile = filepath.Join(t.TempDir(), "private")
	s.preferences.Workspace = t.TempDir()
	previous := s.preferences.Workspace
	if err := s.prepareEnvironment(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(environmentWorkspace(s.profile)) })
	if s.environmentDir() == previous || s.environmentDir() != environmentWorkspace(s.profile) || s.workspace() != previous {
		t.Fatal("environment identity followed the selected project")
	}
	if _, err := validateWorkspace(s.environmentDir(), s.profile); err != nil {
		t.Fatal(err)
	}
	var saved Preferences
	b, err := os.ReadFile(filepath.Join(s.profile, "workbench.json"))
	if err != nil || json.Unmarshal(b, &saved) != nil || saved.Workspace != s.workspace() {
		t.Fatal("environment workspace was not persisted")
	}
}

func TestEnvironmentEntryPreservesLiveProject(t *testing.T) {
	s := testServer(t)
	s.preferences.Workspace, _ = filepath.EvalSymlinks(t.TempDir())
	workspace := s.workspace()
	path := filepath.Join(s.profile, "state", "lima", cage.InstanceName(s.profile, workspace), "cage-running.json")
	if err := writePrivate(filepath.Join(filepath.Dir(path), "lima.yaml"), map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if err := writePrivate(path, map[string]int{"pid": os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	if err := s.prepareEnvironment(); err != nil {
		t.Fatal(err)
	}
	if s.workspace() != workspace || s.environmentDir() != workspace {
		t.Fatal("live project was replaced")
	}
}

func TestBrowserHomeValidationAndDefault(t *testing.T) {
	s := testServer(t)
	cfg, _ := loadConfig(s.configPath)
	input := settingsView(cfg, s.preferences)
	if input.BrowserHomeURL != defaultBrowserHomeURL {
		t.Fatal("legacy preferences did not default to Bing")
	}
	for _, value := range []string{"chrome://new-tab-page-third-party/", "javascript:alert(1)", "file:///private", "https://user:secret@example.com/", "https://", "bing.com"} {
		input.BrowserHomeURL = value
		if _, _, err := proposedSettings(input, cfg, s.profile); err == nil {
			t.Fatalf("accepted invalid home %q", value)
		}
	}
	input.BrowserHomeURL = " https://example.com/start?q=hello "
	_, p, err := proposedSettings(input, cfg, s.profile)
	if err != nil || p.BrowserHomeURL != "https://example.com/start?q=hello" {
		t.Fatalf("valid home: %v", err)
	}
}

func TestBrowserHomeSavesWithoutRestartAndRoutesNewPages(t *testing.T) {
	s := testServer(t)
	cfg, _ := loadConfig(s.configPath)
	s.ready = true
	s.credentials.Token = "private-guest-token"
	var paths []string
	s.browserControl = browserClientForTest(t, s, browserTransport(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		var input struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if input.URL != "https://example.com/start" {
			t.Fatalf("incorrect home sent: %q", input.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"page"}`)), Header: http.Header{}}, nil
	}))
	p := s.preferences
	p.BrowserHomeURL = "https://example.com/start"
	if err := s.apply(savedProfile{cfg, p}, true); err != nil {
		t.Fatal(err)
	}
	if !s.ready {
		t.Fatal("preference change stopped the environment")
	}
	w := request(s, "POST", "/api/browser/new", s.origin, `{}`, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Join(paths, ",") != "/homepage,/new" {
		t.Fatalf("unexpected lifecycle or browser requests: %v", paths)
	}
	b, err := os.ReadFile(filepath.Join(s.profile, "workbench.json"))
	var saved Preferences
	if err != nil || json.Unmarshal(b, &saved) != nil || saved.BrowserHomeURL != p.BrowserHomeURL {
		t.Fatal("home was not persisted")
	}
}
