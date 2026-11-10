package workbench

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sylvinus/agent-vm/internal/guestos"
)

func TestMacOSPhoneRequiresCurrentRoomAndAccountUnlock(t *testing.T) {
	s := testServer(t)
	s.macosGuest = &guestos.MacOSProfile{Instance: "mac", User: "cage"}
	workspace := t.TempDir()
	if err := writePrivate(filepath.Join(s.profile, "environment.json"), map[string]string{"directory": workspace}); err != nil {
		t.Fatal(err)
	}
	room, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s.phones.record = &phoneRecord{}
	s.phones.roomCtx, s.phones.roomProof = room, "current-private-room"
	path := "/api/mobile/cli/projects"
	for _, proof := range []string{"", "expired-private-room"} {
		if response := windowsPhoneRequest(t, s, http.MethodGet, path, "", proof); response.Code != http.StatusForbidden {
			t.Fatalf("untrusted phone admitted: %d", response.Code)
		}
	}
	response := windowsPhoneRequest(t, s, http.MethodGet, path, "", s.phones.roomProof)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"shared_sessions":true`) {
		t.Fatalf("Mac phone could not read shared projects: %d %s", response.Code, response.Body.String())
	}
	if response := windowsPhoneRequest(t, s, http.MethodGet, "/api/settings", "", s.phones.roomProof); response.Code != http.StatusForbidden {
		t.Fatal("phone proof allowed desktop administration")
	}
	s.account.trustedSession = true
	if response := windowsPhoneRequest(t, s, http.MethodGet, path, "", s.phones.roomProof); response.Code != http.StatusLocked {
		t.Fatal("phone inherited a desktop-only account unlock")
	}
	s.account.trustedSession = false
	cancel()
	if response := windowsPhoneRequest(t, s, http.MethodGet, path, "", s.phones.roomProof); response.Code != http.StatusForbidden {
		t.Fatal("revoked phone retained access")
	}
}

func TestMacOSPhoneMediaUsesBoundGuestProject(t *testing.T) {
	s := testServer(t)
	s.macosGuest = &guestos.MacOSProfile{Instance: "mac", User: "cage"}
	workspace := t.TempDir()
	guest := s.cliWorkspace(workspace)
	name, err := mediaFileName(guest, guest, guest+"/images/example.png")
	if err != nil || name != filepath.Join("images", "example.png") {
		t.Fatalf("guest media did not resolve within host project: %q %v", name, err)
	}
	if _, err := mediaFileName(guest, guest, guest+"/../outside.png"); err == nil {
		t.Fatal("guest media escaped its bound project")
	}
}

func TestMacOSPhoneBrowserPreservesPublicNetworkDenial(t *testing.T) {
	s := &Server{macosGuest: &guestos.MacOSProfile{User: "cage"}}
	s.phones.browser.nativeBrowser = fixtureWindowsBrowserClient(t)
	_, status, err := s.phoneSurfaceGuest(context.Background(), phoneBrowser, "", "frame", phoneSurfaceInput{ID: "550e8400-e29b-41d4-a716-446655440000"})
	if err != nil || status != http.StatusForbidden {
		t.Fatalf("native public-network denial changed: %d %v", status, err)
	}
	if !macOSWorkbenchRoute("/api/mobile/browser/550e8400-e29b-41d4-a716-446655440000/frame") {
		t.Fatal("native phone browser route remains unavailable")
	}
}
