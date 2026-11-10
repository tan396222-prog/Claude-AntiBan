package workbench

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/guestos"
)

func init() {
	if os.Getenv("CAGE_TEST_WINDOWS_COMPONENT_RELAY") != "1" {
		return
	}
	if len(os.Args) != 4 || os.Args[1] != "--relay-profile" || os.Args[3] != "status" {
		os.Exit(2)
	}
	binding, err := guestos.LoadWindowsProfile(filepath.Dir(os.Args[2]))
	if err != nil || binding == nil {
		os.Exit(2)
	}
	vm := binding.VMID.String()
	if os.Getenv("CAGE_TEST_WINDOWS_COMPONENT_BAD_IDENTITY") == "1" {
		vm = uuid.NewString()
	}
	// This is an explicit transport fixture, not a Windows COM or package test.
	report := windowsGuestState{Version: 1, OS: "windows", VMID: vm, SID: binding.GUIUserSID, Session: "component-fixture", Available: true,
		Capabilities: []string{"cli", "desktop", "audio"}, Components: map[string]windowsComponentState{
			"cli":     {Installed: false, Ready: false, Reason: "cli_tools_missing_or_invalid", Dependencies: map[string]bool{"python": false}},
			"desktop": {Installed: true, Ready: true}, "audio": {Supported: true, Installed: true, Ready: false, Reason: "audio_status_unavailable", Stage: "render_enumerate", NativeCode: "80004002"},
		}}
	_ = json.NewEncoder(os.Stdout).Encode(report)
	os.Exit(0)
}

func TestWindowsStatusRefreshUsesFixedPairedStatusTransport(t *testing.T) {
	s := testServer(t)
	binding := guestos.WindowsProfile{Version: 1, VMID: uuid.New(), GUIUserSID: "S-1-5-21-1-2-3-1001", GUIUser: "Guest", InstanceDirectory: filepath.Join(s.profile, "unused-vm"), PairingFile: filepath.Join(s.profile, "unused-pair"), RelayExecutable: os.Args[0]}
	if err := guestos.BindWindowsProfile(s.profile, binding); err != nil {
		t.Fatal(err)
	}
	s.windowsGuest = &binding
	s.ready = true
	s.windowsState = windowsGuestState{Version: 1, OS: "windows", VMID: binding.VMID.String(), SID: binding.GUIUserSID, Session: "initial", Available: true}
	t.Setenv("CAGE_TEST_WINDOWS_COMPONENT_RELAY", "1")
	wait := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			s.mu.Lock()
			done := !s.windowsState.ComponentsRefreshing && !s.windowsState.ComponentsChecked.IsZero()
			s.mu.Unlock()
			if done {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("fixed status refresh did not complete")
	}
	if w := windowsBrowserRequestForTest(s, http.MethodGet, "/api/status", nil); w.Code != http.StatusOK {
		t.Fatalf("actual status route: %d", w.Code)
	}
	wait()
	s.mu.Lock()
	available := s.windowsCapabilitiesLocked()["available"].(map[string]bool)
	session := s.windowsState.Session
	s.mu.Unlock()
	if !available["claude-desktop"] || available["claude-cli"] || available["audio"] || session != "component-fixture" {
		t.Fatal("actual fixed status transport did not replace pending component data")
	}
	t.Setenv("CAGE_TEST_WINDOWS_COMPONENT_BAD_IDENTITY", "1")
	s.mu.Lock()
	s.windowsState.ComponentsChecked = time.Time{}
	s.mu.Unlock()
	if w := windowsBrowserRequestForTest(s, http.MethodGet, "/api/status", nil); w.Code != http.StatusOK {
		t.Fatalf("actual refresh route: %d", w.Code)
	}
	wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.windowsState.VMID != binding.VMID.String() || s.windowsState.Session != "component-fixture" || s.windowsState.Components["desktop"].Ready || s.windowsState.Components["desktop"].Reason != "agent_status_unavailable" {
		t.Fatal("wrong VM status gained readiness or replaced authenticated identity")
	}
}

func TestWindowsStatusSeparatesRegisteredFromReadyComponents(t *testing.T) {
	s := testServer(t)
	s.windowsGuest = &guestos.WindowsProfile{}
	s.ready = true
	s.windowsState.Available = true
	s.windowsState.ComponentsChecked = time.Now()
	s.windowsState.Capabilities = []string{"cli", "desktop", "audio", "terminal", "clipboard"}
	s.windowsState.Components = map[string]windowsComponentState{
		"cli":      {Installed: false, Ready: false, Reason: "cli_tools_missing_or_invalid", Dependencies: map[string]bool{"python": false, "claude": true, "git_bash": true}},
		"desktop":  {Installed: false, Ready: false, Reason: "desktop_not_installed"},
		"audio":    {Supported: true, Installed: true, Ready: false, Reason: "audio_status_unavailable", Stage: "render_activate", NativeCode: "80040154"},
		"terminal": {Installed: true, Ready: true}, "clipboard": {Installed: true, Ready: true},
	}
	w := windowsBrowserRequestForTest(s, http.MethodGet, "/api/status", nil)
	var status struct {
		Capabilities struct {
			Registered  map[string]bool                  `json:"registered"`
			Installed   map[string]bool                  `json:"installed"`
			Available   map[string]bool                  `json:"available"`
			Components  map[string]windowsComponentState `json:"components"`
			Unavailable []string                         `json:"unavailable"`
		} `json:"guest_capabilities"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &status) != nil {
		t.Fatalf("actual status handler: %d", w.Code)
	}
	for _, name := range []string{"cli", "desktop", "audio"} {
		if !status.Capabilities.Registered[name] {
			t.Fatalf("RPC registration lost: %s", name)
		}
	}
	for _, name := range []string{"claude-cli", "claude-desktop", "audio", "desktop-file-chooser", "desktop-downloads", "phone-browser", "phone-desktop", "legacy-mobile-migration"} {
		if status.Capabilities.Available[name] {
			t.Fatalf("unready component advertised: %s", name)
		}
		found := false
		for _, missing := range status.Capabilities.Unavailable {
			if missing == name {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing capability omitted: %s", name)
		}
	}
	if !status.Capabilities.Installed["audio"] || status.Capabilities.Installed["claude-cli"] || status.Capabilities.Installed["claude-desktop"] {
		t.Fatal("installed prerequisites conflated with registration")
	}
	if status.Capabilities.Components["audio"].Stage != "render_activate" || status.Capabilities.Components["audio"].NativeCode != "80040154" || status.Capabilities.Components["cli"].Dependencies["python"] {
		t.Fatal("safe diagnostic/dependency status lost")
	}

	s.windowsState.Components["desktop"] = windowsComponentState{Installed: true, Ready: true}
	s.windowsState.Components["cli"] = windowsComponentState{Installed: true, Ready: true}
	available := s.windowsCapabilitiesLocked()["available"].(map[string]bool)
	if !available["claude-desktop"] || !available["claude-cli"] {
		t.Fatal("verified installed components must remain usable before on-demand app start")
	}
	s.windowsState.Available = false
	available = s.windowsCapabilitiesLocked()["available"].(map[string]bool)
	if available["claude-cli"] || available["claude-desktop"] || available["terminal"] {
		t.Fatal("locked GUI retained readiness")
	}
	s.windowsState.Available = true
	s.windowsState.Capabilities = []string{"desktop"}
	if s.windowsCapabilitiesLocked()["available"].(map[string]bool)["claude-cli"] || !s.windowsCapabilitiesLocked()["installed"].(map[string]bool)["claude-cli"] {
		t.Fatal("newly installed CLI without registered handler must require agent reload")
	}
	s.windowsState.ComponentsChecked = time.Now().Add(-time.Minute)
	if s.windowsCapabilitiesLocked()["available"].(map[string]bool)["claude-desktop"] {
		t.Fatal("old component cache remained ready without active status polling")
	}
}
