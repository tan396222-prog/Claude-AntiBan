package appupdate

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/diagnostics"
)

func TestUpdateCheckWaitsForProxyButDownloadReportsFailure(t *testing.T) {
	for _, operation := range []string{"check", "download"} {
		t.Run(operation, func(t *testing.T) {
			profile := t.TempDir()
			config := filepath.Join(profile, "config.json")
			data, _ := json.Marshal(cage.DefaultConfig())
			if err := os.WriteFile(config, data, 0600); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			code := Run(config, []string{operation}, &output)
			var state State
			if err := json.Unmarshal(output.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
			if operation == "check" {
				if code != 0 || state.Phase != "waiting" || !state.Retryable {
					t.Fatalf("expected deferred check: code=%d %+v", code, state)
				}
				info, err := diagnostics.New(profile).Info()
				if err != nil || info.Bytes != 0 {
					t.Fatalf("ordinary wait logged as failure: %+v %v", info, err)
				}
			} else if code == 0 || state.Phase != "failed" {
				t.Fatalf("requested download must report unavailable proxy: code=%d %+v", code, state)
			}
		})
	}
}
