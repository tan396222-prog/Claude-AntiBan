package guestos

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWindowsLifecycleExactVMAndSafeShutdown(t *testing.T) {
	shell, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("PowerShell not installed")
	}
	query, err := filepath.Abs("windows_lifecycle.ps1")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", filepath.Join("testdata", "windows_lifecycle.ps1"), "-Query", query)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("lifecycle mock failed: %v\n%s", err, output)
	}
}
