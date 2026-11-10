package workbench

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sylvinus/agent-vm/internal/diagnostics"
)

func diagnosticContents(t *testing.T, data []byte) map[string]string {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]string{}
	for _, file := range reader.File {
		input, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(input)
		input.Close()
		if err != nil {
			t.Fatal(err)
		}
		result[file.Name] = string(content)
	}
	return result
}

func TestDiagnosticExportsIncludeSeparateDesktopLog(t *testing.T) {
	profile := t.TempDir()
	desktop := filepath.Join(profile, "desktop-entry")
	if err := diagnostics.New(profile).Record("error", "controller", "workload-sentinel"); err != nil {
		t.Fatal(err)
	}
	if err := diagnostics.New(desktop).Record("error", "frontend", "frontend-sentinel password=private-secret"); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "offline.zip")
	if err := ExportDiagnostics(filepath.Join(profile, "config.json"), output); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	contents := diagnosticContents(t, data)
	if !strings.Contains(contents["logs/events.jsonl"], "workload-sentinel") || !strings.Contains(contents["desktop/logs/events.jsonl"], "frontend-sentinel") {
		t.Fatal("offline export omitted one log owner")
	}
	if strings.Contains(contents["desktop/logs/events.jsonl"], "private-secret") {
		t.Fatal("desktop diagnostic secret was not redacted")
	}
	var workload bytes.Buffer
	if err := diagnostics.New(profile).Export(&workload, "test", nil, "offline"); err != nil {
		t.Fatal(err)
	}
	s := &Server{profile: desktop}
	response := &http.Response{Body: io.NopCloser(bytes.NewReader(workload.Bytes())), Header: make(http.Header), Request: &http.Request{URL: &url.URL{Path: "/api/diagnostics/export"}}}
	if err := s.addDesktopDiagnosticResponse(response); err != nil {
		t.Fatal(err)
	}
	combined, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.ContentLength != int64(len(combined)) || !strings.Contains(diagnosticContents(t, combined)["desktop/logs/events.jsonl"], "frontend-sentinel") {
		t.Fatal("gateway export omitted desktop errors or returned the old content length")
	}
}

func TestDiagnosticMergeRejectsPathsOutsideFixedExport(t *testing.T) {
	for _, name := range []string{"../outside", "logs/../../outside.jsonl", `logs/..\outside.jsonl`, "project.json"} {
		var input bytes.Buffer
		writer := zip.NewWriter(&input)
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = file.Write([]byte("untrusted"))
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := diagnosticBundleWithDesktop(input.Bytes(), t.TempDir()); err == nil {
			t.Fatalf("untrusted ZIP entry accepted: %q", name)
		}
	}
}
