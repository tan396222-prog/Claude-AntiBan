package workbench

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sylvinus/agent-vm/internal/guestos"
)

func TestVMRecordWithReusedPIDIsNotRunning(t *testing.T) {
	for _, guest := range []string{"linux", "macos"} {
		t.Run(guest, func(t *testing.T) {
			s := testServer(t)
			s.preferences.Workspace, _ = filepath.EvalSymlinks(t.TempDir())
			if guest == "macos" {
				s.macosGuest = &guestos.MacOSProfile{Instance: "mac", User: "cage"}
			}
			dir := s.instanceDir()
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			cfg, err := loadConfig(s.configPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := cfg.RecordRunningAt(dir); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "cage-running.json")
			var record map[string]any
			b, _ := os.ReadFile(path)
			if err := json.Unmarshal(b, &record); err != nil {
				t.Fatal(err)
			}
			started, _ := record["started"].(string)
			if started == "" {
				t.Skip("process start time is unavailable on this platform")
			}
			if !s.vmRunning() {
				t.Fatal("current owner was not recognized")
			}
			if owners, err := controllerVMOwners(s.profile); err != nil || len(owners) != 1 {
				t.Fatalf("owners = %v, %v", owners, err)
			}
			record["started"] = started + "0"
			if err := writePrivate(path, record); err != nil {
				t.Fatal(err)
			}
			if s.vmRunning() {
				t.Fatal("record from an earlier process with the same PID was treated as running")
			}
			if owners, err := controllerVMOwners(s.profile); err != nil || len(owners) != 0 {
				t.Fatalf("stale owners = %v, %v", owners, err)
			}
			delete(record, "started")
			if err := writePrivate(path, record); err != nil {
				t.Fatal(err)
			}
			if !s.vmRunning() {
				t.Fatal("legacy PID-only record was rejected")
			}
		})
	}
}
