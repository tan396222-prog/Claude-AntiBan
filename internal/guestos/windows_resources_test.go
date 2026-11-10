package guestos

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWindowsResourceExactVMAndRollback(t *testing.T) {
	shell, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("PowerShell not installed")
	}
	query, err := filepath.Abs("windows_resources.ps1")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", filepath.Join("testdata", "windows_resources.ps1"), "-Query", query)
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Windows resource script failed: %v\n%s", err, data)
	}
}
