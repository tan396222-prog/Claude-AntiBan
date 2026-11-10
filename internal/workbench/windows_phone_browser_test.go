package workbench

import (
	"context"
	"github.com/sylvinus/agent-vm/internal/guestos"
	"testing"
	"time"
)

func TestWindowsPhoneBrowserPrivateRelayPreservesPublicPolicyDenial(t *testing.T) {
	client := fixtureWindowsBrowserClient(t)
	s := &Server{windowsGuest: &guestos.WindowsProfile{}}
	s.phones.browser.nativeBrowser = client
	_, status, err := s.phoneSurfaceGuest(context.Background(), phoneBrowser, "unused-private-token", "frame", phoneSurfaceInput{ID: "550e8400-e29b-41d4-a716-446655440000"})
	if err != nil || status != 403 {
		t.Fatalf("phone public-only status lost: %d %v", status, err)
	}
	if !windowsWorkbenchRoute("/api/mobile/browser/550e8400-e29b-41d4-a716-446655440000/frame") || !validWindowsBrowserRoute("POST", "/phone-browser/frame") || validWindowsBrowserRoute("GET", "/phone-browser/open") {
		t.Fatal("Windows phone route contract is invalid")
	}
}

func TestWindowsPhoneBrowserCapabilityKeepsMissingDependenciesAndGateClosedUnavailable(t *testing.T) {
	s := &Server{ready: true, windowsState: windowsGuestState{Available: true, ComponentsChecked: time.Now(), Capabilities: []string{"browser"}, Components: map[string]windowsComponentState{"phone-browser": {Supported: true, Registered: true, Installed: true, Ready: true}}}}
	s.windowsState.Browser.PhoneNetworkConfigured = true
	state := s.windowsCapabilitiesLocked()
	if state["installed"].(map[string]bool)["phone-browser"] || state["available"].(map[string]bool)["phone-browser"] {
		t.Fatal("phone component registration hid missing dependencies")
	}
	s.windowsState.Browser.PhoneInstalled = true
	state = s.windowsCapabilitiesLocked()
	if !state["registered"].(map[string]bool)["phone-browser"] || !state["installed"].(map[string]bool)["phone-browser"] || state["available"].(map[string]bool)["phone-browser"] {
		t.Fatal("closed Gate was reported as a usable phone browser")
	}
	if state["components"].(map[string]windowsComponentState)["phone-browser"].Reason == "not_implemented" {
		t.Fatal("implemented phone browser still reported unfinished")
	}
	if state["available"].(map[string]bool)["phone-desktop"] {
		t.Fatal("phone desktop was silently enabled")
	}
}
