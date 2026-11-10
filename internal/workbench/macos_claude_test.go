package workbench

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sylvinus/agent-vm/internal/guestos"
)

func TestMacOSClaudeRouteCannotRequestProjectPanel(t *testing.T) {
	s := testServer(t)
	body := `{"action":"project-frame","project":"/Users/guest/Cage/Projects/aaaaaaaaaaaaaaaaaaaaaaaa"}`
	// A project grant must be checked by the separate approval route.
	for _, test := range []struct {
		profile *guestos.MacOSProfile
		status  int
	}{
		{nil, http.StatusNotFound},
		{&guestos.MacOSProfile{Instance: "mac", User: "guest"}, http.StatusBadRequest},
	} {
		s.macosGuest = test.profile
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/macos/claude/window", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		s.macOSClaudeWindow(response, request)
		if response.Code != test.status {
			t.Fatalf("got %d, want %d", response.Code, test.status)
		}
	}
}
