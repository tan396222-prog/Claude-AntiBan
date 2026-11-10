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

	"github.com/google/uuid"
)

func TestMobileCLIRecoveryDoesNotReplayAndChangesEpoch(t *testing.T) {
	s := testServer(t)
	project, _ := filepath.EvalSymlinks(t.TempDir())
	if err := writePrivate(filepath.Join(s.profile, "environment.json"), map[string]string{"directory": project}); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	old, err := s.mobileCLI.create(s.profile, project, id)
	if err != nil {
		t.Fatal(err)
	}
	lookup, err := s.mobileCLI.record(s.profile, id)
	if err != nil {
		t.Fatal(err)
	}
	if lookup.CLIID == id {
		t.Fatal("caller selected the runtime transcript ID")
	}
	lookup.Turns = append(lookup.Turns, mobileTurn{ID: uuid.NewString(), Prompt: "test accepted request", Status: "running"})
	lookup.Resume = true
	lookup.changed()
	path := mobileRecordPath(s.profile, id)
	if err = writePrivate(path, lookup); err != nil {
		t.Fatal(err)
	}
	s.mobileCLI = mobileCLIStore{}
	url := "/api/mobile/cli/sessions/" + id
	w := request(s, "GET", url+"?after=3&epoch="+old.Generation, "", "", true)
	if w.Code != 200 {
		t.Fatalf("recovery returned %d", w.Code)
	}
	var recovered mobileSession
	if err = json.Unmarshal(w.Body.Bytes(), &recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.Generation == old.Generation || recovered.Turns[0].Status != "interrupted" {
		t.Fatal("unfinished turn was not reconciled")
	}
	message, _ := json.Marshal(map[string]string{"request_id": lookup.Turns[0].ID, "text": lookup.Turns[0].Prompt})
	if rep := request(s, "POST", url+"/messages", s.origin, string(message), true); rep.Code != 200 {
		t.Fatal("same request should resolve to its accepted result", rep.Code)
	}
	message, _ = json.Marshal(map[string]string{"request_id": lookup.Turns[0].ID, "text": "other prompt"})
	if rep := request(s, "POST", url+"/messages", s.origin, string(message), true); rep.Code != 409 {
		t.Fatal("request ID reused for different content")
	}
	if err = os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.mobileCLI.create(s.profile, project, id); err == nil {
		t.Fatal("corrupted record was overwritten")
	}
}

func TestMobileCLIAccountAndConnectionBoundaries(t *testing.T) {
	s := testServer(t)
	path := "/api/mobile/cli/projects"
	if w := request(s, "GET", path, "", "", false); w.Code != 401 {
		t.Fatal("unauthenticated project read", w.Code)
	}
	if w := request(s, "POST", "/api/mobile/cli/sessions", "https://outside.example", "{}", true); w.Code != 403 {
		t.Fatal("cross-origin create", w.Code)
	}
	s.remote = newRemoteGateway(s.profile)
	s.remote.setRemote(true)
	// Even if the desktop switches into SSH mode the mobile link is for this
	// host. Disconnected SSH routing would reject this request with HTTP 409.
	if w := request(s, "GET", path, "", "", true); w.Code != 200 {
		t.Fatal("mobile route escaped host scope", w.Code)
	}
	s.clientOnly = true
	if w := request(s, "GET", path, "", "", true); w.Code != 409 {
		t.Fatal("client-only host allowed work", w.Code)
	}
	s.clientOnly = false
	s.account.lock()
	if w := request(s, "GET", path, "", "", true); w.Code != http.StatusLocked {
		t.Fatal("locked account allowed read", w.Code)
	}
}

func TestMobileCLILaunchScopeAndOldApproval(t *testing.T) {
	for _, command := range []string{"claude --dangerously-skip-permissions", "ccs a;id", "ccs --yes", "bash -c anything", "claude | other"} {
		if _, err := mobileLaunchArgs(command, claudeSelection{}); err == nil {
			t.Fatal("accepted launcher", command)
		}
	}
	for _, command := range []string{"claude", "ccs klink"} {
		if _, err := mobileLaunchArgs(command, claudeSelection{}); err != nil {
			t.Fatal(err)
		}
	}
	s := testServer(t)
	project, _ := filepath.EvalSymlinks(t.TempDir())
	if err := writePrivate(filepath.Join(s.profile, "environment.json"), map[string]string{"directory": project}); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if _, err := s.mobileCLI.create(s.profile, project, id); err != nil {
		t.Fatal(err)
	}
	record, err := s.mobileCLI.record(s.profile, id)
	if err != nil {
		t.Fatal(err)
	}
	turnID, approvalID := uuid.NewString(), uuid.NewString()
	record.Turns = []mobileTurn{{ID: turnID, Status: "running", Approval: &mobileApproval{ID: approvalID, Tool: "Bash"}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	decision := &mobileDecision{ctx: ctx, reply: make(chan bool, 1)}
	s.mobileCLI.active = map[string]*mobileRun{id: {record: record, pending: decision, cancel: cancel}}
	answer := func(approval string) int {
		body, _ := json.Marshal(map[string]any{"request_id": turnID, "approval_id": approval, "allow": true})
		return request(s, "POST", "/api/mobile/cli/sessions/"+id+"/approval", s.origin, string(body), true).Code
	}
	if answer(uuid.NewString()) != 409 {
		t.Fatal("stale approval accepted")
	}
	if answer(approvalID) != 200 || !<-decision.reply {
		t.Fatal("exact approval not delivered")
	}
	if answer(approvalID) != 409 {
		t.Fatal("decision replay accepted")
	}
}

func TestDesktopCLIViewUsesOnlyItsLocalWorkload(t *testing.T) {
	gateway, local := gatewayFixture(t)
	gateway.desktopEntry = true
	d := gateway.remote.desktop
	generation := chooseLocal(t, d, d.context().ConnectionID)
	path := "/api/mobile/cli/projects"
	if w := gatewayRequest(gateway, path, generation); w.Code != http.StatusOK || local.commands.Load() != 1 {
		t.Fatalf("desktop local view failed: %d, requests %d", w.Code, local.commands.Load())
	}
	for _, id := range []string{"", "stale-generation"} {
		if w := gatewayRequest(gateway, path, id); w.Code != http.StatusConflict || local.commands.Load() != 1 {
			t.Fatalf("stale view reached workload: %d", w.Code)
		}
	}
	d.mu.Lock()
	d.active.mode = "remote"
	d.mu.Unlock()
	if w := gatewayRequest(gateway, path, generation); w.Code != http.StatusForbidden || local.commands.Load() != 1 {
		t.Fatalf("mobile operation sent to SSH workload: %d", w.Code)
	}
	if w := gatewayRequest(gateway, "/api/mobile-pair", generation); w.Code != http.StatusForbidden || local.commands.Load() != 1 {
		t.Fatalf("phone pairing management reached the SSH workload: %d", w.Code)
	}
}

func TestMobileCLIStopFromPreviousUnlock(t *testing.T) {
	s := testServer(t)
	project, _ := filepath.EvalSymlinks(t.TempDir())
	if err := writePrivate(filepath.Join(s.profile, "environment.json"), map[string]string{"directory": project}); err != nil {
		t.Fatal(err)
	}
	id, turnID := uuid.NewString(), uuid.NewString()
	if _, err := s.mobileCLI.create(s.profile, project, id); err != nil {
		t.Fatal(err)
	}
	record, err := s.mobileCLI.record(s.profile, id)
	if err != nil {
		t.Fatal(err)
	}
	record.Turns = []mobileTurn{{ID: turnID, Status: "running"}}
	run, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.mobileCLI.active = map[string]*mobileRun{id: {record: record, cancel: cancel}}
	stop := func(epoch context.Context) int {
		req := httptest.NewRequest("POST", "/", strings.NewReader(`{"request_id":"`+turnID+`"}`))
		req.SetPathValue("id", id)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.mobileStop(w, req.WithContext(context.WithValue(req.Context(), mobileEpochKey{}, epoch)))
		return w.Code
	}
	oldEpoch, _ := s.account.access()
	s.account.lock()
	s.account.setKey(make([]byte, 32))
	if code := stop(oldEpoch); code != http.StatusLocked || run.Err() != nil {
		t.Fatalf("expired request stopped active turn: HTTP %d, %v", code, run.Err())
	}
	newEpoch, _ := s.account.access()
	if code := stop(newEpoch); code != http.StatusOK || run.Err() == nil {
		t.Fatalf("current request failed to stop active turn: HTTP %d, %v", code, run.Err())
	}
}
