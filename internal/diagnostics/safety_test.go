package diagnostics

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportSafetyEvidenceRedactsAndExcludesPrivateFields(t *testing.T) {
	profile := t.TempDir()
	report := `{"policy":"cage-safety/2","created_at":"2026-11-07T04:00:00Z","configuration":"private-config-hash","run_id":"private-run-id","future_secret":"never-export","checks":[{"id":"guest_settings","title":"系统时区与语言","state":"failed","critical":true,"evidence":"locale invalid token=sample-private-token /home/alice/private 203.0.113.8","remedy":"重新应用设置","checked_at":"2026-11-07T04:00:01Z"}]}`
	if err := os.WriteFile(filepath.Join(profile, "last-safety-report.json"), []byte(report), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := New(profile).Export(&out, "test", nil, "offline"); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range z.File {
		if f.Name != "safety/latest-report.json" {
			continue
		}
		found = true
		r, _ := f.Open()
		data, _ := io.ReadAll(r)
		r.Close()
		var value safetyEvidence
		if json.Unmarshal(data, &value) != nil || len(value.Checks) != 1 || value.Checks[0].State != "failed" || !strings.Contains(value.Checks[0].Evidence, "locale invalid") {
			t.Fatalf("evidence lost or JSON damaged: %s", data)
		}
		for _, secret := range []string{"private-config-hash", "private-run-id", "never-export", "sample-private-token", "alice", "203.0.113.8"} {
			if bytes.Contains(data, []byte(secret)) {
				t.Fatalf("export leaked %s", secret)
			}
		}
	}
	if !found {
		t.Fatal("offline export omitted saved safety report")
	}
	if err := os.WriteFile(filepath.Join(profile, "last-safety-report.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := New(profile).Export(&out, "test", nil, "offline"); err != nil {
		t.Fatal("invalid report blocked host logs", err)
	}
}
