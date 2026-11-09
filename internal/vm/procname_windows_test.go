package vm

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsQEMUIdentityHandlesSpacesAndRequiresInstance(t *testing.T) {
	dir := `C:\Users\测试 User\.claude-cage\state\lima\test`
	args := []string{`C:\Program Files\Cage\resources\runtime\qemu\qemu-system-x86_64.exe`, "-name", "lima-test", "-pidfile", dir + `\qemu.pid`}
	if !windowsQEMUCommandMatches(windows.ComposeCommandLine(args), dir, "test") {
		t.Fatal("quoted Unicode QEMU identity not recognized")
	}
	if windowsQEMUCommandMatches(windows.ComposeCommandLine(args), dir, "other") {
		t.Fatal("another instance was accepted")
	}
	args[4] = dir + `-other\qemu.pid`
	if windowsQEMUCommandMatches(windows.ComposeCommandLine(args), dir, "test") {
		t.Fatal("another instance PID file was accepted")
	}
}
