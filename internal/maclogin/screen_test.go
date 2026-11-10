package maclogin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLockedConsoleBoundToOneUserAndSession(t *testing.T) {
	const session = "607bceb6-c53f-4253-bad4-9c22b4ae7325"
	const row = "<dict><key>kCGSSessionOnConsoleKey</key><true/><key>kCGSessionLoginDoneKey</key><true/><key>CGSSessionScreenIsLocked</key><true/><key>kCGSSessionUserIDKey</key><integer>502</integer><key>kCGSSessionUserNameKey</key><string>trial</string><key>CGSSessionUniqueSessionUUID</key><string>" + session + "</string></dict>"
	const other = "<dict><key>kCGSSessionOnConsoleKey</key><false/></dict>"
	makeProbe := func(header, rows string) string { return header + "\n<plist><array>" + rows + "</array></plist>" }
	valid := makeProbe("502 trial 502", row)
	for _, tc := range []struct{ label, input, want string }{
		{"system login", "system-login", "system-login"},
		{"locked", valid, "locked-" + session},
		{"logged-out second user", makeProbe("502 trial 502", other+row), "locked-" + session},
		{"unlocked", strings.Replace(valid, "ScreenIsLocked</key><true/>", "ScreenIsLocked</key><false/>", 1), ""},
		{"missing lock value", strings.Replace(valid, "<key>CGSSessionScreenIsLocked</key><true/>", "", 1), ""},
		{"wrong lock type", strings.Replace(valid, "ScreenIsLocked</key><true/>", "ScreenIsLocked</key><string>true</string>", 1), ""},
		{"different console", makeProbe("503 other 502", row), ""},
		{"different profile UID", makeProbe("503 trial 502", row), ""},
		{"different registry UID", strings.Replace(valid, "<integer>502", "<integer>503", 1), ""},
		{"different registry user", strings.Replace(valid, "<string>trial", "<string>other", 1), ""},
		{"not logged in", strings.Replace(valid, "LoginDoneKey</key><true/>", "LoginDoneKey</key><false/>", 1), ""},
		{"ambiguous", makeProbe("502 trial 502", row+row), ""},
		{"missing session identity", strings.Replace(valid, session, "", 1), ""},
		{"missing active flag", strings.Replace(valid, "<key>kCGSSessionOnConsoleKey</key><true/>", "", 1), ""},
		{"bad plist", valid + "<", ""},
		{"no session", makeProbe("502 trial 502", other), ""},
	} {
		t.Run(tc.label, func(t *testing.T) {
			if got := permittedLoginScreen([]byte(tc.input), "trial"); got != tc.want {
				t.Fatalf("allowed screen %q; want %q", got, tc.want)
			}
		})
	}
}

func TestOffscreenViewMatchesDirectoryAliases(t *testing.T) {
	instance := t.TempDir()
	alias := filepath.Join(t.TempDir(), "lima-mac")
	if err := os.Symlink(instance, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if !sameGuestDirectory(alias, instance) || sameGuestDirectory(t.TempDir(), instance) || sameGuestDirectory(filepath.Join(alias, "absent"), filepath.Join(instance, "absent")) {
		t.Fatal("offscreen view must be bound to an existing instance, including its socket path alias")
	}
}
