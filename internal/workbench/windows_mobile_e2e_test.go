package workbench

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/guestos"
)

// This native opt-in probe uses its own host profile, pairing and project. It
// opens the real GUI user's ConPTY without typing a prompt or starting login.
// It proves native API dispatch, not a logged-in turn or model response.
func TestWindowsMobileNativePairingNoPromptE2E(t *testing.T) {
	profile := os.Getenv("CAGE_WINDOWS_GUEST_QA_PROFILE")
	if runtime.GOOS != "windows" || profile == "" || os.Getenv("CAGE_TEST_WINDOWS_MOBILE_NATIVE") != "1" {
		t.Skip("requires an explicitly paired isolated native Windows guest")
	}
	binding, err := guestos.LoadWindowsProfile(profile)
	if err != nil || binding == nil {
		t.Fatalf("paired Windows QA profile: %v", err)
	}
	s := testServer(t)
	if err := guestos.BindWindowsProfile(s.profile, *binding); err != nil {
		t.Fatal(err)
	}
	s.windowsGuest = binding
	s.ready = true
	s.preferences = Preferences{Keyboard: "native", ClaudeTerminalCommand: "claude"}
	s.lifetimeContext()
	defer s.lifeCancel()
	defer s.terminals.close()
	defer s.closeProjectShares()
	ctx, cancel := context.WithTimeout(s.lifetimeContext(), 20*time.Second)
	defer cancel()
	state, err := s.windowsStatus(ctx)
	if err != nil || !slices.Contains(state.Capabilities, "cli") {
		t.Fatalf("actual GUI/native CLI unavailable: %v", err)
	}
	for {
		ready := state.Available
		for _, name := range []string{"cli", "terminal", "project-mount"} {
			component := state.Components[name]
			ready = ready && component.Installed && component.Ready
		}
		if ready {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("actual GUI native CLI/terminal/project components never became ready")
		case <-time.After(time.Second):
		}
		fresh, err := readWindowsComponentStatus(ctx, s.profile)
		if err != nil || fresh.Version != 1 || fresh.OS != "windows" || fresh.VMID != binding.VMID.String() || fresh.SID != binding.GUIUserSID || fresh.Session == "" {
			t.Fatalf("actual native component identity/readiness: %v", err)
		}
		fresh.Running = state.Running
		state = fresh
	}
	s.windowsState = state
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := writePrivate(filepath.Join(s.profile, "environment.json"), map[string]string{"directory": workspace}); err != nil {
		t.Fatal(err)
	}
	client, phoneCtx := pairedWindowsPhoneForTest(t, s)
	creation := uuid.NewString()
	encoded, _ := json.Marshal(map[string]any{"id": creation, "workspace": workspace})
	reply, err := client.Do(phoneCtx, http.MethodPost, "/api/mobile/cli/sessions", encoded)
	if err != nil || reply.Status != http.StatusOK {
		t.Fatalf("real phone/native ConPTY launch: %v %#v", err, reply)
	}
	var session mobileSession
	if json.Unmarshal(reply.Body, &session) != nil || session.ID != creation || session.DesktopTerminalID == "" {
		t.Fatal("phone did not receive the same desktop terminal")
	}
	phoneView, err := client.Do(phoneCtx, http.MethodGet, "/api/mobile/cli/sessions/"+creation, nil)
	if err != nil || phoneView.Status != http.StatusOK {
		t.Fatalf("native phone view: %v %#v", err, phoneView)
	}
	desktopView := windowsBrowserRequestForTest(s, http.MethodGet, "/api/cli/sessions/"+creation, nil)
	var phone, desktop mobileSession
	if desktopView.Code != http.StatusOK || json.Unmarshal(phoneView.Body, &phone) != nil || json.Unmarshal(desktopView.Body.Bytes(), &desktop) != nil || phone.ID != desktop.ID || phone.DesktopTerminalID != desktop.DesktopTerminalID {
		t.Fatal("phone/desktop native execution identity differs")
	}
	t.Logf("actual shared execution opened; model-send readiness=%v (no prompt or login sent)", phone.CanSend != nil && *phone.CanSend)
	// Revoke only this test's mount; subsequent native reads must reject it.
	s.closeProjectShares()
	denied, err := client.Do(phoneCtx, http.MethodGet, "/api/mobile/cli/sessions/"+creation, nil)
	if err == nil && denied.Status == http.StatusOK {
		t.Fatal("native revoked project remained readable")
	}
}
