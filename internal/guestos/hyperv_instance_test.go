package guestos

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestHyperVReadOnlyQuery(t *testing.T) {
	shell := "pwsh"
	if runtime.GOOS == "windows" {
		shell = "powershell.exe"
	}
	binary, err := exec.LookPath(shell)
	if err != nil {
		t.Skipf("%s is not installed", shell)
	}
	query, err := filepath.Abs("hyperv_instance.ps1")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), binary, "-NoProfile", "-NonInteractive", "-File", filepath.Join("testdata", "hyperv_instance.ps1"), "-Query", query)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Hyper-V query: %v\n%s", err, output)
	}
}
