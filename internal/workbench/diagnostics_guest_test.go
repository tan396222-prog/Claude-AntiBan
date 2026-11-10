package workbench

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMacOSGuestDiagnosticCollectorBoundsAndFilters(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("macOS guest collector requires POSIX directory descriptors and file ownership")
	}
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv is required for the guest collector")
	}
	home := t.TempDir()
	directory := filepath.Join(home, "Library", "Logs", "Cage")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	var events strings.Builder
	stamp := time.Now().UTC().Format(time.RFC3339)
	for index := 0; index < 150; index++ {
		fmt.Fprintf(&events, "{\"time\":%q,\"source\":\"window\",\"reason\":\"frame_unavailable\"}\n", stamp)
	}
	if err := os.WriteFile(filepath.Join(directory, "window-1.jsonl"), []byte(events.String()), 0600); err != nil {
		t.Fatal(err)
	}
	events.Reset()
	fmt.Fprintf(&events, "{\"time\":%q,\"source\":\"window\",\"reason\":\"after_rotation\"}\n", stamp)
	fmt.Fprintln(&events, `{"time":"2020-01-01T00:00:00Z","source":"window","reason":"old_event"}`)
	fmt.Fprintf(&events, "{\"time\":%q,\"source\":\"window\",\"reason\":\"password=private-secret\"}\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintln(&events, "invalid JSON")
	fmt.Fprintln(&events, `{"time":123,"source":"window","reason":"invalid_timestamp"}`)
	if err := os.WriteFile(filepath.Join(directory, "window.jsonl"), []byte(events.String()), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("uv", "run", "--no-project", "python", "-I", "-c", macOSDiagnosticCollector)
	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home)
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Version int    `json:"version"`
		Lines   string `json:"lines"`
	}
	if json.Unmarshal(output, &result) != nil || result.Version != 1 || strings.Count(result.Lines, "\n") != 150 || len(result.Lines) > 48<<10 || strings.Contains(result.Lines, "private-secret") || strings.Contains(result.Lines, "old_event") {
		t.Fatalf("guest diagnostics escaped the bounded fixed schema: %s", output)
	}
	if !strings.Contains(result.Lines, "after_rotation") {
		t.Fatal("missing newest event from current file after same-second rotation")
	}
}
