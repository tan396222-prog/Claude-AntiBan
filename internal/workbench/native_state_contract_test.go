package workbench

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/sylvinus/agent-vm/internal/guestos"
)

func TestNativeStatusExposesMaintenanceFailureAndRestoreRoute(t *testing.T) {
	for _, system := range []string{guestos.MacOS, guestos.Windows11} {
		t.Run(system, func(t *testing.T) {
			s := testServer(t)
			if system == guestos.MacOS {
				s.macosGuest = &guestos.MacOSProfile{Instance: "mac"}
			} else {
				s.windowsGuest = &guestos.WindowsProfile{}
			}
			cfg, err := loadConfig(s.configPath)
			if err != nil {
				t.Fatal(err)
			}
			previous := savedProfile{Config: cfg, Preferences: s.preferences}
			previous.Preferences.Keyboard = "native"
			if err := writePrivate(filepath.Join(s.profile, "previous-profile.json"), previous); err != nil {
				t.Fatal(err)
			}
			s.guestUpdateRequired = true
			s.guestUpdateError = "同步未完成，可保留原环境重试"
			response := windowsBrowserRequestForTest(s, "GET", "/api/status", nil)
			var state struct {
				System         string `json:"guest_system"`
				CanRestore     bool   `json:"can_restore"`
				UpdateRequired bool   `json:"environment_update_required"`
				UpdateError    string `json:"environment_update_error"`
				LocalStorage   bool   `json:"local_vm_storage"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &state) != nil {
				t.Fatal(response.Code, response.Body.String())
			}
			if state.System != system || !state.CanRestore || !state.UpdateRequired || state.UpdateError != s.guestUpdateError || !state.LocalStorage {
				t.Fatalf("native state diverged from executable operations: %+v", state)
			}
			if storage := windowsBrowserRequestForTest(s, "GET", "/api/environment/storage", nil); storage.Code != http.StatusOK {
				t.Fatal("advertised storage operation rejected by route", storage.Code, storage.Body.String())
			}
			response = windowsBrowserRequestForTest(s, "POST", "/api/restore", []byte(`{}`))
			if response.Code != http.StatusOK {
				t.Fatal("advertised restore rejected by native route", response.Code, response.Body.String())
			}
			job := awaitJobIdle(t, &s.jobs)
			if job.Kind != "restore" {
				t.Fatal("restore did not use the common operation", job)
			}
			var restored Preferences
			data, err := os.ReadFile(filepath.Join(s.profile, "workbench.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			if restored.Keyboard != "native" {
				t.Fatal("normal restore route did not persist the prior preference")
			}
			// There is deliberately no VM in this fixture. Starting it may fail,
			// after the requested configuration has reached its normal entry point.
		})
	}
}
