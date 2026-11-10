package workbench

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestMacOSProjectWaitsForLoginAndRestoresAutomatically(t *testing.T) {
	var loggedIn atomic.Bool
	guest := newMacWorkspaceGuest(t, func(channel ssh.Channel, command string) (uint32, bool) {
		if strings.Contains(command, "/dev/console") && !loggedIn.Load() {
			return 73, true
		}
		return 0, false
	})
	guest.approved.Store(true)
	s := guest.s
	s.restoreProjectShares()
	if len(s.projectErrors) != 1 || s.projectErrors[0].Recovery != "guest-login" || len(s.projectShares) != 0 {
		t.Fatalf("missing actionable login state: %+v", s.projectErrors)
	}
	data, _ := json.Marshal(map[string]any{"workspace": guest.hostPath, "add": false})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/projects/open", bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	s.openProject(recorder, request)
	if recorder.Code != http.StatusBadRequest || recorder.Header().Get("X-Cage-Recovery") != "guest-login" {
		t.Fatalf("project open lost its recovery action: %d %s", recorder.Code, recorder.Body.String())
	}
	s.retryMacOSProjectShares(t.Context())
	if len(s.projectShares) != 0 {
		t.Fatal("mount started before guest GUI login")
	}
	loggedIn.Store(true)
	s.retryMacOSProjectShares(t.Context())
	defer s.closeProjectShares()
	if len(s.projectErrors) != 0 || s.projectShares[guest.hostPath] == nil {
		t.Fatalf("login did not restore the saved project: %+v", s.projectErrors)
	}
}

func TestMacOSProjectPrepareReportsSpecificRecovery(t *testing.T) {
	for _, sample := range []struct {
		exit     uint32
		recovery string
	}{
		{73, "guest-login"}, {74, "environment-update"}, {76, "environment-restart"},
		{77, "project-directory"}, {78, "project-directory"}, {79, "environment-update"}, {255, "project-retry"},
	} {
		t.Run(sample.recovery, func(t *testing.T) {
			guest := newMacWorkspaceGuest(t, func(channel ssh.Channel, command string) (uint32, bool) {
				if strings.Contains(command, "/dev/console") {
					return sample.exit, true
				}
				return 0, false
			})
			guest.s.restoreProjectShares()
			if len(guest.s.projectErrors) != 1 || guest.s.projectErrors[0].Recovery != sample.recovery {
				t.Fatalf("exit %d: %+v", sample.exit, guest.s.projectErrors)
			}
		})
	}
}

func TestMacOSFailedUnsavedProjectDoesNotBecomeBackgroundGrant(t *testing.T) {
	guest := newMacWorkspaceGuest(t, func(channel ssh.Channel, command string) (uint32, bool) {
		if strings.Contains(command, "/dev/console") {
			return 73, true
		}
		return 0, false
	})
	if _, err := guest.s.connectProject(t.TempDir(), true); err == nil {
		t.Fatal("expected the guest login precondition")
	}
	if len(guest.s.projectErrors) != 0 || len(guest.s.projectShares) != 0 {
		t.Fatal("an uncompleted new project was scheduled for background sharing")
	}
}
