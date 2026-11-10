package workbench

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sylvinus/agent-vm/internal/guestos"
)

func TestProjectAuthorizationProtectsCredentialsAcrossGuests(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	credentials := filepath.Join(home, ".ssh")
	siblingState := filepath.Join(home, ".claude-cage", "state")
	for _, directory := range []string{credentials, siblingState} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, system := range []string{"linux", "macos", "windows"} {
		t.Run(system, func(t *testing.T) {
			s := testServer(t)
			s.ready = true
			if system == "macos" {
				s.macosGuest = &guestos.MacOSProfile{Instance: "mac", User: "guestdev"}
			}
			if system == "windows" {
				s.windowsGuest = &guestos.WindowsProfile{}
			}
			for _, directory := range []string{credentials, siblingState} {
				body, _ := json.Marshal(map[string]any{"workspace": directory, "add": true})
				response := windowsBrowserRequestForTest(s, http.MethodPost, "/api/projects/open", body)
				if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "共享") {
					t.Fatalf("credential project admission: status=%d body=%s", response.Code, response.Body.String())
				}
				paths, err := s.projectPaths()
				if err != nil || slices.Contains(paths, directory) || len(s.projectShares) != 0 {
					t.Fatalf("rejected project retained a grant or connection: %v", err)
				}
				response = windowsBrowserRequestForTest(s, http.MethodGet, "/api/projects/directories?path="+url.QueryEscape(directory), nil)
				if response.Code == http.StatusOK {
					var listing struct{ Selectable bool }
					if json.Unmarshal(response.Body.Bytes(), &listing) != nil || listing.Selectable {
						t.Fatal("directory browser offered a protected project")
					}
				}
			}
		})
	}
}

func TestProjectRestoreRevalidatesExistingCredentialGrant(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	s := testServer(t)
	credentials := filepath.Join(home, ".ssh")
	if err := os.Mkdir(credentials, 0700); err != nil {
		t.Fatal(err)
	}
	// Existing versions could persist this grant. A new connection must apply
	// the current authorization rule before starting a transport.
	if err := s.rememberSharedProject(credentials); err != nil {
		t.Fatal(err)
	}
	s.restoreProjectShares()
	if len(s.projectShares) != 0 || len(s.projectErrors) != 1 || s.projectErrors[0].Path != credentials {
		t.Fatalf("old credential grant was not rejected on restore: %+v", s.projectErrors)
	}
	if !strings.Contains(s.projectErrors[0].Message, "凭据") {
		t.Fatalf("restore did not report the authorization failure: %+v", s.projectErrors)
	}
}
