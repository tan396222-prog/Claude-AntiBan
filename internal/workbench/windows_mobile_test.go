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

	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/mobilelink"
	"github.com/sylvinus/agent-vm/internal/relay"
	"github.com/sylvinus/agent-vm/internal/remotelink"
)

func windowsMobileFixture(t *testing.T) (*Server, *mobileRecord, func()) {
	t.Helper()
	s := windowsCLIRelayFixture(t)
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := writePrivate(filepath.Join(s.profile, "environment.json"), map[string]string{"directory": workspace}); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if _, err := s.mobileCLI.create(s.profile, workspace, id); err != nil {
		t.Fatal(err)
	}
	record, err := s.mobileCLI.record(s.profile, id)
	if err != nil {
		t.Fatal(err)
	}
	record.Terminal = true
	record.Connection = uuid.NewString()
	record.GuestConfig = `C:\Users\Guest\.claude`
	record.GuestWorkspace = `C:\Users\Guest\Cage\Projects\` + strings.Repeat("a", 16)
	if err := writePrivate(mobileRecordPath(s.profile, id), record); err != nil {
		t.Fatal(err)
	}
	s.projectShares = map[string]*projectShare{workspace: {windowsProject: &windowsProjectBinding{ID: strings.Repeat("a", 16), Grant: strings.Repeat("b", 64), Path: record.GuestWorkspace}, done: make(chan struct{})}}
	room, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s.phones.record = &phoneRecord{}
	s.phones.roomCtx, s.phones.roomProof = room, "current-private-room"
	s.ready = true
	s.windowsState.Available = true
	s.windowsState.ComponentsChecked = time.Now()
	s.windowsState.Capabilities = []string{"cli", "terminal", "project-mount"}
	s.windowsState.Components = map[string]windowsComponentState{"cli": {Installed: true, Ready: true}, "terminal": {Installed: true, Ready: true}, "project-mount": {Installed: true, Ready: true}}
	t.Setenv("CAGE_WINDOWS_CLI_RELAY_MOBILE", "1")
	t.Setenv("CAGE_WINDOWS_CLI_RELAY_WORKSPACE", record.GuestWorkspace)
	return s, record, cancel
}

func windowsPhoneRequest(t *testing.T, s *Server, method, path, body, proof string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, s.origin+path, strings.NewReader(body))
	r.Header.Set("Origin", s.origin)
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: s.cookieName(), Value: s.session})
	if proof != "" {
		r.Header.Set(mobilelink.PhoneRoomHeader, proof)
	}
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, r)
	return w
}

func TestWindowsMobileHandlerRequiresCurrentRoomAndOrdinaryAccountUnlock(t *testing.T) {
	s, record, cancel := windowsMobileFixture(t)
	path := "/api/mobile/cli/sessions/" + record.ID
	for _, proof := range []string{"", "old-private-room"} {
		if w := windowsPhoneRequest(t, s, "GET", path, "", proof); w.Code != http.StatusForbidden {
			t.Fatalf("untrusted room admitted: %d", w.Code)
		}
	}
	if w := windowsPhoneRequest(t, s, "GET", path, "", s.phones.roomProof); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"can_send":true`) {
		t.Fatalf("actual native dispatch failed: %d %s", w.Code, w.Body.String())
	}
	s.account.trustedSession = true
	if w := windowsPhoneRequest(t, s, "GET", path, "", s.phones.roomProof); w.Code != http.StatusLocked {
		t.Fatalf("phone gained desktop-only unlock: %d", w.Code)
	}
	s.account.trustedSession = false
	cancel()
	if w := windowsPhoneRequest(t, s, "GET", path, "", s.phones.roomProof); w.Code != http.StatusForbidden {
		t.Fatalf("revoked room admitted: %d", w.Code)
	}
}

func TestWindowsMobileProjectsReflectInstalledNativeHandlers(t *testing.T) {
	s, _, _ := windowsMobileFixture(t)
	for _, installed := range []bool{true, false} {
		if !installed {
			s.windowsState.Capabilities = nil
		}
		w := windowsPhoneRequest(t, s, "GET", "/api/mobile/cli/projects", "", s.phones.roomProof)
		var state struct {
			Shared bool `json:"shared_sessions"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &state) != nil || state.Shared != installed {
			t.Fatalf("native capability reporting: %d", w.Code)
		}
	}
}

func TestWindowsMobilePurgeUsesLiveHostGrantInsteadOfHostGuestPath(t *testing.T) {
	s, record, _ := windowsMobileFixture(t)
	record.Archived = true
	if err := writePrivate(mobileRecordPath(s.profile, record.ID), record); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(t.TempDir(), "purge-relay.jsonl")
	t.Setenv("CAGE_WINDOWS_CLI_RELAY_CAPTURE", capture)
	w := windowsPhoneRequest(t, s, "POST", "/api/mobile/cli/sessions/"+record.ID+"/purge", `{"confirm":true}`, s.phones.roomProof)
	if w.Code != http.StatusOK {
		t.Fatalf("native purge dispatch: %d %s", w.Code, w.Body.String())
	}
	data, err := os.ReadFile(capture)
	if err != nil || !strings.Contains(string(data), `"mode":"purge"`) {
		t.Fatal("purge did not reach fixed native relay")
	}
	stored, err := s.mobileCLI.record(s.profile, record.ID)
	if err != nil || !stored.Purged {
		t.Fatal("confirmed native purge did not persist its tombstone")
	}
}

func TestWindowsMobileHandlerUsesHostRecordGrantAndRejectsPhoneGuestPaths(t *testing.T) {
	s, record, _ := windowsMobileFixture(t)
	capture := filepath.Join(t.TempDir(), "native-requests.jsonl")
	t.Setenv("CAGE_WINDOWS_CLI_RELAY_CAPTURE", capture)
	requestID := uuid.NewString()
	path := "/api/mobile/cli/sessions/" + record.ID + "/messages"
	body := `{"request_id":"` + requestID + `","text":"中文 fixture"}`
	w := windowsPhoneRequest(t, s, "POST", path, body, s.phones.roomProof)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"acknowledged_request_id":"`+requestID+`"`) {
		t.Fatalf("native receipt failed: %d %s", w.Code, w.Body.String())
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"mode":"rpc"`) || !strings.Contains(string(data), strings.Repeat("b", 64)) {
		t.Fatal("real relay child did not receive host grant")
	}
	if strings.Contains(w.Body.String(), record.GuestWorkspace) || strings.Contains(w.Body.String(), strings.Repeat("b", 64)) {
		t.Fatal("native path/grant leaked to phone")
	}
	guestBody, _ := json.Marshal(map[string]any{"id": uuid.NewString(), "workspace": record.GuestWorkspace})
	if w := windowsPhoneRequest(t, s, "POST", "/api/mobile/cli/sessions", string(guestBody), s.phones.roomProof); w.Code != http.StatusForbidden {
		t.Fatalf("phone selected guest directory: %d", w.Code)
	}
	other, err := s.mobileCLI.create(s.profile, t.TempDir(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if w := windowsPhoneRequest(t, s, "GET", "/api/mobile/cli/sessions/"+other.ID, "", s.phones.roomProof); w.Code != http.StatusForbidden {
		t.Fatalf("ungranted project history exposed: %d", w.Code)
	}
	close(s.projectShares[record.Workspace].done)
	if w := windowsPhoneRequest(t, s, "GET", "/api/mobile/cli/sessions/"+record.ID, "", s.phones.roomProof); w.Code == http.StatusOK {
		t.Fatal("revoked project reached native view")
	}
}

func TestWindowsLegacyMobileLaunchPreservesHistoryWithoutLinuxFallback(t *testing.T) {
	s, record, _ := windowsMobileFixture(t)
	record.Terminal = false
	record.Resume = true
	record.Turns = []mobileTurn{{ID: uuid.NewString(), Prompt: "kept history", Text: "kept answer"}}
	if err := writePrivate(mobileRecordPath(s.profile, record.ID), record); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.prepareMobileLaunch(record); err != errWindowsLegacyMobile {
		t.Fatalf("legacy Windows fallback: %v", err)
	}
	w := windowsPhoneRequest(t, s, "POST", "/api/mobile/cli/sessions/"+record.ID+"/open", `{"automatic":true}`, s.phones.roomProof)
	if w.Code != http.StatusConflict {
		t.Fatalf("legacy Linux record executed: %d", w.Code)
	}
	data, err := os.ReadFile(mobileRecordPath(s.profile, record.ID))
	if err != nil || !strings.Contains(string(data), "kept answer") {
		t.Fatal("legacy history changed")
	}
}

func TestWindowsMobileRoomRevocationCancelsInFlightNativeRequest(t *testing.T) {
	s, record, cancel := windowsMobileFixture(t)
	marker := filepath.Join(t.TempDir(), "native-started")
	t.Setenv("CAGE_WINDOWS_CLI_RELAY_BLOCK", marker)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- windowsPhoneRequest(t, s, "GET", "/api/mobile/cli/sessions/"+record.ID, "", s.phones.roomProof)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("native relay child did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case result := <-done:
		if result.Code == http.StatusOK {
			t.Fatal("revoked native read succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("phone revocation left native request running")
	}
}

func TestWindowsMobileMediaMapsOnlyCurrentLiveProject(t *testing.T) {
	s, record, _ := windowsMobileFixture(t)
	guestRoot := record.GuestWorkspace
	record.GuestWorkspace += `\nested`
	record.Turns = []mobileTurn{{ID: uuid.NewString(), Text: `![frame](frame.png) ` + guestRoot + `\nested\frame.png ../outside.png`}}
	if err := writePrivate(mobileRecordPath(s.profile, record.ID), record); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(record.Workspace, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	content := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 16)...)
	if err := os.WriteFile(filepath.Join(record.Workspace, "nested", "frame.png"), content, 0600); err != nil {
		t.Fatal(err)
	}
	for _, reference := range []string{"frame.png", guestRoot + `\nested\frame.png`} {
		body, _ := json.Marshal(mediaChunkRequest{Reference: reference})
		w := windowsPhoneRequest(t, s, "POST", "/api/mobile/cli/sessions/"+record.ID+"/media", string(body), s.phones.roomProof)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"mime":"image/png"`) {
			t.Fatalf("native media path: %d %s", w.Code, w.Body.String())
		}
	}
	for _, reference := range []string{`C:\outside\frame.png`, `..\..\outside.png`, `\\server\share\frame.png`, `C:frame.png`, guestRoot + `\frame.png:stream`} {
		if _, err := windowsMediaFileName(guestRoot, record.GuestWorkspace, reference); err == nil {
			t.Fatal("guest media escaped project")
		}
	}
	close(s.projectShares[record.Workspace].done)
	body, _ := json.Marshal(mediaChunkRequest{Reference: "frame.png"})
	if w := windowsPhoneRequest(t, s, "POST", "/api/mobile/cli/sessions/"+record.ID+"/media", string(body), s.phones.roomProof); w.Code != http.StatusConflict {
		t.Fatalf("revoked media mount accepted: %d", w.Code)
	}
}

func TestWindowsMobileRealPairingTransportReachesNativeSession(t *testing.T) {
	s, record, cancelOld := windowsMobileFixture(t)
	cancelOld()
	s.phones.record = nil
	s.phones.roomCtx = nil
	s.phones.roomProof = ""
	client, ctx := pairedWindowsPhoneForTest(t, s)
	reply, err := client.Do(ctx, "GET", "/api/mobile/cli/sessions/"+record.ID, nil)
	if err != nil || reply.Status != http.StatusOK || !strings.Contains(string(reply.Body), `"can_send":true`) {
		t.Fatalf("encrypted phone/native session: %v %#v", err, reply)
	}
	s.phones.mu.Lock()
	privateProof := s.phones.roomProof
	s.phones.mu.Unlock()
	if strings.Contains(string(reply.Body), record.GuestWorkspace) || strings.Contains(string(reply.Body), privateProof) {
		t.Fatal("private backend capability reached phone")
	}
	if w := windowsBrowserRequestForTest(s, "DELETE", "/api/mobile-pair", []byte(`{}`)); w.Code != http.StatusOK {
		t.Fatalf("pair removal: %d", w.Code)
	}
	if reply, err := client.Do(ctx, "GET", "/api/mobile/cli/sessions/"+record.ID, nil); err == nil && reply.Status == http.StatusOK {
		t.Fatal("revoked encrypted phone kept session access")
	}
}

func pairedWindowsPhoneForTest(t *testing.T, s *Server) (*mobilelink.Client, context.Context) {
	t.Helper()
	edge, err := relay.New(relay.Config{PublicRooms: true, AdminToken: strings.Repeat("x", 32)})
	if err != nil {
		t.Fatal(err)
	}
	relayHTTP := httptest.NewServer(edge)
	t.Cleanup(func() { edge.Close(); relayHTTP.Close() })
	server := httptest.NewUnstartedServer(s.handler())
	s.origin = "http://" + server.Listener.Addr().String()
	server.Start()
	t.Cleanup(server.Close)
	t.Cleanup(s.stopPhone)
	s.preferences.MobileRelay = relayHTTP.URL
	response := windowsBrowserRequestForTest(s, "POST", "/api/mobile-pair", []byte(`{}`))
	var invitation struct{ Manual string }
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &invitation) != nil {
		t.Fatalf("actual pairing route: %d", response.Code)
	}
	phone, err := remotelink.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	encoded, err := mobilelink.RedeemPair(ctx, invitation.Manual, relayHTTP.URL, phone.Pin)
	if err != nil {
		t.Fatal(err)
	}
	var pairing mobilelink.PairedHost
	if json.Unmarshal([]byte(encoded), &pairing) != nil {
		t.Fatal("pairing decode")
	}
	var client *mobilelink.Client
	for ctx.Err() == nil {
		room, err := mobilelink.LookupRoom(ctx, relayHTTP.URL, pairing.Lookup)
		if err == nil {
			client, err = mobilelink.ConnectClient(ctx, relayHTTP.URL, room.ID, room.ClientToken, phone, pairing.HostPin)
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if client == nil {
		t.Fatal("paired room did not start")
	}
	t.Cleanup(func() { _ = client.Close() })
	return client, ctx
}
