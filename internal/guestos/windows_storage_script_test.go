package guestos

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWindowsStoragePowerShellTransition(t *testing.T) {
	command := "pwsh"
	if runtime.GOOS == "windows" {
		command = "powershell.exe"
	}
	powershell, err := exec.LookPath(command)
	if err != nil {
		t.Skip("requires PowerShell for the native storage command contract")
	}
	source, err := filepath.Abs("windows_storage.ps1")
	if err != nil {
		t.Fatal(err)
	}
	test, err := filepath.Abs(filepath.Join("testdata", "windows_storage.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", test, "-Implementation", source).CombinedOutput()
	if err != nil {
		t.Fatalf("native storage contract: %v\n%s", err, output)
	}
	t.Log(string(output))
}
