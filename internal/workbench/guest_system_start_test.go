package workbench

import (
	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/guestos"
	"net/http"
	"strings"
	"testing"
)

func TestUnsupportedGuestSystemCannotStartDebian(t *testing.T) {
	s := testServer(t)
	for _, system := range []string{"macos", "windows-11", "debian-12", "ubuntu-22.04", "ubuntu-24.04", "unknown"} {
		response := request(s, http.MethodPost, "/api/start", s.origin, `{"guest_system":"`+system+`"}`, true)
		if response.Code != http.StatusConflict || s.jobs.snapshot().ID != "" {
			t.Fatalf("%s: got %d, job %v", system, response.Code, s.jobs.snapshot())
		}
	}
}

func TestIncompleteInstallationPreventsImplicitLinuxStart(t *testing.T) {
	s := testServer(t)
	if err := guestos.SaveInstallation(s.profile, guestos.Installation{Version: 1, System: guestos.MacOS, ID: uuid.New()}); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"{}", `{"guest_system":"debian-13"}`} {
		w := request(s, "POST", "/api/start", s.origin, body, true)
		if w.Code != http.StatusConflict || s.jobs.snapshot().ID != "" {
			t.Fatalf("created a Linux job from pending macOS (%s): %d", body, w.Code)
		}
	}
	if err := s.startEnvironment(false); err == nil || !strings.Contains(err.Error(), "原生客户机") {
		t.Fatalf("indirect Linux start: %v", err)
	}
}
