package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sylvinus/agent-vm/internal/mobilelink"
)

func TestNewPairingUsesDesktopRelay(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	prefs := filepath.Join(dir, "workbench.json")
	for _, tc := range []struct{ name, json, want string }{
		{"new profile", "", mobilelink.OfficialRelay},
		{"existing profile", `{"keyboard":"mac"}`, mobilelink.OfficialRelay},
		{"custom relay", `{"mobile_relay":"https://relay.example:8443"}`, "https://relay.example:8443"},
		{"damaged preferences", "{", ""},
		{"insecure relay", `{"mobile_relay":"http://relay.example"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.json != "" {
				if err := os.WriteFile(prefs, []byte(tc.json), 0600); err != nil {
					t.Fatal(err)
				}
			}
			origin, err := profileRelay(config)
			if origin != tc.want || (err != nil) != (tc.want == "") {
				t.Fatalf("origin = %q, error = %v", origin, err)
			}
		})
	}
}
