//go:build windows

package guestos

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sylvinus/agent-vm/internal/diagnostics"
)

func TestWindowsHostRepairPreservesProcessOutput(t *testing.T) {
	profile := t.TempDir()
	if err := BindWindowsProfile(profile, windowsBindingFixture(profile)); err != nil {
		t.Fatal(err)
	}
	kit := nativeManifestKit(t, Windows11)
	script := `param([string]$Profile,[string]$ManifestPath,[string]$ManifestSHA256,[switch]$HostOnly)
$ErrorActionPreference='Stop'
[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false)
if (!$HostOnly -or (Get-FileHash -LiteralPath $ManifestPath -Algorithm SHA256).Hash -ne $ManifestSHA256) { exit 38 }
Write-Output 'repair-fixture-stdout'
[Console]::Error.WriteLine('repair-fixture-service-registration-failed token=repair-fixture-secret')
exit 37
`
	if err := os.WriteFile(filepath.Join(kit, "maintain-windows-workbench.ps1"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	out := diagnostics.NewStream(nil, "storage-recovery", "info", func(line string) { stdout.WriteString(line) })
	errOut := diagnostics.NewStream(nil, "storage-recovery", "warn", func(line string) { stderr.WriteString(line) })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := RepairWindowsHostManager(ctx, profile, kit, out, errOut)
	_ = out.Close()
	_ = errOut.Close()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 37 {
		t.Fatalf("repair lost the original process result: %v", err)
	}
	if !strings.Contains(stdout.String(), "repair-fixture-stdout") || !strings.Contains(stderr.String(), "repair-fixture-service-registration-failed") {
		t.Fatalf("repair output did not reach the shared diagnostic streams: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "repair-fixture-secret") || !strings.Contains(stderr.String(), "[redacted]") {
		t.Fatal("repair output bypassed diagnostic redaction")
	}
	remaining, err := filepath.Glob(filepath.Join(profile, "native-host-repair-*.json"))
	if err != nil || len(remaining) != 0 {
		t.Fatalf("repair manifest was retained after failure: %v %v", remaining, err)
	}
}
