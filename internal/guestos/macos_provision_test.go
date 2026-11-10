package guestos

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMacOSProvisionIdentity(t *testing.T) {
	profile := t.TempDir()
	dir := filepath.Join(profile, "state", "lima", "mac")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ config, want string }{
		{"Host lima-mac\n User cage\n Port 12345\n", "cage"},
		{"Host lima-mac\n User cageprovision\n Port 12345\n", "cageprovision"},
		{"User cageuser\n", ""}, {"User root\n", ""}, {"Port 12345\n", ""},
		{"User cage\nUser cageprovision\n", ""}, {"User cage extra\n", ""},
	} {
		if err := os.WriteFile(filepath.Join(dir, "ssh.config"), []byte(tc.config), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := MacOSProvisionUser(profile)
		if got != tc.want || (err == nil) != (tc.want != "") {
			t.Fatalf("%q: got %q, %v", tc.config, got, err)
		}
	}
}
