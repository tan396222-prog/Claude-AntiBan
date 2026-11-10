package workbench

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/guestos"
)

func removeProjectRequest(t *testing.T, s *Server, path string, want int) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"workspace": path})
	response := windowsBrowserRequestForTest(s, "POST", "/api/projects/remove", body)
	if response.Code != want {
		t.Fatalf("remove status=%d want=%d body=%s", response.Code, want, response.Body.String())
	}
}

func TestProjectRemovalPersistsRevocationAcrossGuests(t *testing.T) {
	for _, system := range []string{"linux", "macos", "windows"} {
		t.Run(system, func(t *testing.T) {
			s := testServer(t)
			if system == "macos" {
				s.macosGuest = &guestos.MacOSProfile{Instance: "mac", User: "guestdev"}
			}
			if system == "windows" {
				s.windowsGuest = &guestos.WindowsProfile{}
			}
			root, _ := filepath.EvalSymlinks(t.TempDir())
			other, _ := filepath.EvalSymlinks(t.TempDir())
			file := filepath.Join(root, "keep.txt")
			if err := os.WriteFile(file, []byte("keep project data"), 0600); err != nil {
				t.Fatal(err)
			}
			s.preferences = Preferences{Workspace: root, Keyboard: "native", AutoLanguage: true}
			before := s.preferences
			for _, path := range []string{root, other} {
				if err := s.rememberSharedProject(path); err != nil {
					t.Fatal(err)
				}
			}
			id := uuid.NewString()
			record := &mobileRecord{mobileSession: mobileSession{mobileSummary: mobileSummary{ID: id, Workspace: root, Title: "saved history"}, Turns: []mobileTurn{}}}
			if err := writePrivate(mobileRecordPath(s.profile, id), record); err != nil {
				t.Fatal(err)
			}
			history, _ := os.ReadFile(mobileRecordPath(s.profile, id))
			detached := 0
			done := make(chan struct{})
			close(done)
			share := &projectShare{done: done, detach: func() { detached++ }}
			s.projectShares = map[string]*projectShare{root: share, other: {}}
			s.projectErrors = []projectShareError{{Path: root, Message: "old failure"}, {Path: other, Message: "keep failure"}}
			s.terminals.sessions = map[string]*terminalSession{"other": {id: "other", workspace: other}}
			s.mobileCLI.running = map[string]bool{id: true}
			s.mobileCLI.catalog = map[string]mobileSummary{id: {ID: id, Workspace: root}}
			removeProjectRequest(t, s, root, 200)
			if detached != 1 || s.projectShares[root] != nil || s.projectShares[other] == nil {
				t.Fatal("wrong share was detached")
			}
			if s.terminals.sessions["other"].closed || len(s.projectErrors) != 1 || s.projectErrors[0].Path != other {
				t.Fatal("unrelated project was changed")
			}
			before.Workspace = s.environmentDir()
			if s.preferences != before {
				t.Fatal("preferences changed beyond current project")
			}
			saved, err := os.ReadFile(filepath.Join(s.profile, "workbench.json"))
			var prefs Preferences
			if err != nil || json.Unmarshal(saved, &prefs) != nil || prefs != before {
				t.Fatal("current project was not persisted", err)
			}
			reloaded := &Server{profile: s.profile, preferences: prefs}
			paths, err := reloaded.projectPaths()
			if err != nil || slices.Contains(paths, root) || !slices.Contains(paths, other) {
				t.Fatal("revoked project returned after restart", paths, err)
			}
			if _, err := s.projectWorkspace(root); err == nil {
				t.Fatal("removed grant still authorizes new work")
			}
			content, _ := os.ReadFile(file)
			afterHistory, _ := os.ReadFile(mobileRecordPath(s.profile, id))
			if string(content) != "keep project data" || string(afterHistory) != string(history) {
				t.Fatal("project files or history changed")
			}
			removeProjectRequest(t, s, root, 200)
			if detached != 1 {
				t.Fatal("retry detached twice")
			}
			if err := s.rememberSharedProject(root); err != nil {
				t.Fatal(err)
			}
			sessions, err := s.mobileCLI.summaries(s.profile, map[string]bool{root: true}, false)
			if err != nil || len(sessions) != 1 {
				t.Fatal("re-adding lost history", err)
			}
		})
	}
}

func TestProjectRemovalRejectsActiveUsersAndUntrustedRequests(t *testing.T) {
	for _, kind := range []string{"terminal", "mobile", "observed-cli", "editor", "browser-failure", "operation", "job", "closing", "locked", "origin", "unauthenticated"} {
		t.Run(kind, func(t *testing.T) {
			s := testServer(t)
			root, _ := filepath.EvalSymlinks(t.TempDir())
			if err := s.rememberSharedProject(root); err != nil {
				t.Fatal(err)
			}
			s.projectShares = map[string]*projectShare{root: {}}
			want := 409
			switch kind {
			case "terminal":
				s.terminals.sessions = map[string]*terminalSession{"live": {workspace: root}}
			case "mobile":
				s.mobileCLI.active = map[string]*mobileRun{"live": {record: &mobileRecord{mobileSession: mobileSession{mobileSummary: mobileSummary{Workspace: root}}}}}
			case "observed-cli":
				s.ready = true
				s.preferences.ClaudeTerminalCommand = "custom-shell"
				s.mobileCLI.running = map[string]bool{"live": true}
				s.mobileCLI.catalog = map[string]mobileSummary{"live": {Workspace: root}}
			case "editor", "browser-failure":
				s.ready = true
				s.preferences.ClaudeTerminalCommand = "custom-shell"
				s.browserControl = browserClientForTest(t, s, browserTransport(func(r *http.Request) (*http.Response, error) {
					if r.URL.Path != "/tabs" {
						t.Fatal("unexpected browser operation", r.URL.Path)
					}
					body, _ := json.Marshal(map[string]any{"tabs": []map[string]string{{"kind": "editor", "workspace": root}}})
					status := 200
					if kind == "browser-failure" {
						status = 503
					}
					return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(string(body))), Header: http.Header{}}, nil
				}))
			case "operation":
				s.op.Lock()
				defer s.op.Unlock()
			case "job":
				s.setJobForTest(Job{Running: true})
			case "closing":
				s.closing = true
				want = 503
			case "locked":
				s.account.lock()
				want = 423
			case "origin", "unauthenticated":
				origin, auth := s.origin, true
				if kind == "origin" {
					origin, want = "https://example.com", 403
				} else {
					auth, want = false, 401
				}
				body, _ := json.Marshal(map[string]string{"workspace": root})
				response := request(s, "POST", "/api/projects/remove", origin, string(body), auth)
				if response.Code != want {
					t.Fatal(response.Code, response.Body.String())
				}
			}
			if kind != "origin" && kind != "unauthenticated" {
				removeProjectRequest(t, s, root, want)
			}
			paths, err := s.projectPaths()
			if err != nil || !slices.Contains(paths, root) || s.projectShares[root] == nil {
				t.Fatal("rejected removal changed authorization")
			}
		})
	}
}

func TestProjectRemovalHandlesMissingDirectoriesAndLegacySelection(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved", true: "legacy-current"}[legacy], func(t *testing.T) {
			s := testServer(t)
			path := filepath.Join(t.TempDir(), "directory-already-moved")
			if legacy {
				s.preferences.Workspace = path
			} else if err := s.rememberSharedProject(path); err != nil {
				t.Fatal(err)
			}
			removeProjectRequest(t, s, path, 200)
			paths, err := s.projectPaths()
			if err != nil || slices.Contains(paths, path) {
				t.Fatal("missing directory could not be removed", err)
			}
		})
	}
	s := testServer(t)
	removeProjectRequest(t, s, s.environmentDir(), 400)
	removeProjectRequest(t, s, "", 400)
}

func TestProjectRemovalPreservesGrantWhenPreferencesWriteFails(t *testing.T) {
	s := testServer(t)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	s.preferences.Workspace = root
	s.projectShares = map[string]*projectShare{root: {}}
	if err := os.Mkdir(filepath.Join(s.profile, "workbench.json"), 0700); err != nil {
		t.Fatal(err)
	}
	removeProjectRequest(t, s, root, 400)
	paths, err := s.projectPaths()
	if err != nil || !slices.Contains(paths, root) || s.preferences.Workspace != root || s.projectShares[root] == nil {
		t.Fatal("write failure revoked the current project")
	}
	if err := os.Remove(filepath.Join(s.profile, "workbench.json")); err != nil {
		t.Fatal(err)
	}
	removeProjectRequest(t, s, root, 200)
}

func TestProjectRemovalRejectsDisconnectedOrUnknownVM(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		s := testServer(t)
		project := t.TempDir()
		if err := s.rememberSharedProject(project); err != nil {
			t.Fatal(err)
		}
		record := filepath.Join(s.profile, "state", "lima", "guest", "cage-running.json")
		value := any(map[string]int{"pid": os.Getpid()})
		if malformed {
			value = map[string]string{"pid": "invalid"}
		}
		if err := writePrivate(record, value); err != nil {
			t.Fatal(err)
		}
		removeProjectRequest(t, s, project, 409)
		paths, err := s.projectPaths()
		if err != nil || !slices.Contains(paths, project) {
			t.Fatal("disconnected VM lost authorization")
		}
	}
}

func TestAgentProjectRemovalUsesSharedOwner(t *testing.T) {
	s := agentTestController(t)
	path := t.TempDir()
	if err := s.rememberSharedProject(path); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{"workspace": path})
	if _, err := AgentCall(t.Context(), s.configPath, "project.remove", input); err != nil {
		t.Fatal(err)
	}
	paths, err := s.projectPaths()
	if err != nil || slices.Contains(paths, path) {
		t.Fatal("agent removal did not revoke grant", err)
	}
}

func TestProjectRemovalRejectsRestoredMacOSEditor(t *testing.T) {
	s := testServer(t)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	s.macosGuest = &guestos.MacOSProfile{Instance: "mac", User: "guestdev"}
	s.ready = true
	s.preferences.ClaudeTerminalCommand = "custom-shell"
	if err := s.rememberSharedProject(root); err != nil {
		t.Fatal(err)
	}
	client := fixtureWindowsBrowserClient(t)
	start := client.start
	client.start = func(ctx context.Context) (*exec.Cmd, error) {
		command, err := start(ctx)
		if err == nil {
			command.Env = append(command.Env, "CAGE_BROWSER_EDITOR_WORKSPACE="+filepath.Base(macOSProjectPath(s.macosGuest.User, root)))
		}
		return command, err
	}
	s.windowsBrowserControl = client
	body, err := s.browserAction(t.Context(), "tabs", nil)
	var state struct {
		Tabs []struct {
			Workspace string `json:"workspace"`
		} `json:"tabs"`
	}
	if err != nil || json.Unmarshal(body, &state) != nil || len(state.Tabs) != 1 || state.Tabs[0].Workspace != root {
		t.Fatal("restored editor lost host workspace", string(body), err)
	}
	removeProjectRequest(t, s, root, 409)
}
