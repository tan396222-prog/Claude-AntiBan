package workbench

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sylvinus/agent-vm/internal/diagnostics"
)

func TestDiagnosticRoutesAuthenticateRedactAndExportWhenLocked(t *testing.T) {
	s := testServer(t)
	s.account.lock()
	for _, path := range []string{"/api/diagnostics", "/api/diagnostics/export"} {
		if w := request(s, "GET", path, "", "", false); w.Code != 401 {
			t.Fatalf("anonymous %s: %d", path, w.Code)
		}
	}
	body := `{"source":"frontend","level":"error","message":"token=sample-private-token https://user:pass@example.com/secret Error: UI failed"}`
	if w := request(s, "POST", "/api/diagnostics/events", "http://evil.example", body, true); w.Code != 403 {
		t.Fatal("cross-origin event accepted")
	}
	if w := request(s, "POST", "/api/diagnostics/events", s.origin, body, true); w.Code != 200 {
		t.Fatalf("event rejected: %d %s", w.Code, w.Body.String())
	}
	w := request(s, "GET", "/api/diagnostics/export", "", "", true)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("export failed: %d %s", w.Code, w.Body.String())
	}
	archive, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var content string
	for _, f := range archive.File {
		r, _ := f.Open()
		b, _ := io.ReadAll(r)
		r.Close()
		content += string(b)
	}
	if strings.Contains(content, "sample-private-token") || strings.Contains(content, "example.com") || !strings.Contains(content, "UI failed") {
		t.Fatalf("invalid exported content: %s", content)
	}
	// Invalid event sources cannot impersonate native/controller diagnostics.
	if w := request(s, "POST", "/api/diagnostics/events", s.origin, `{"source":"native","message":"spoof"}`, true); w.Code != 400 {
		t.Fatal("source spoof accepted")
	}
	if w := request(s, "POST", "/api/diagnostics/events", s.origin, `{"source":"frontend","message":"`+strings.Repeat("x", 33<<10)+`"}`, true); w.Code != 400 {
		t.Fatal("oversized event accepted")
	}
	for i := 0; i < 65; i++ {
		w = request(s, "POST", "/api/diagnostics/events", s.origin, body, true)
	}
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("no rate limit: %d", w.Code)
	}
}
func TestOfflineExportPreservesDestinationOnErrorAndNoArchiveSymlink(t *testing.T) {
	profile := t.TempDir()
	path := filepath.Join(profile, "config.json")
	output := filepath.Join(t.TempDir(), "bundle.zip")
	if err := diagnostics.New(profile).Record("error", "native", "password=offline-secret startup failed"); err != nil {
		t.Fatal(err)
	}
	if err := ExportDiagnostics(path, output); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(output)
	if err != nil {
		t.Fatal(err)
	}
	archive.Close()
	original, _ := os.ReadFile(output)
	if err := ExportDiagnostics(path, "relative.zip"); err == nil {
		t.Fatal("relative destination accepted")
	}
	if err := os.Mkdir(filepath.Join(profile, "control.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := ExportDiagnostics(path, output); err != nil {
		t.Fatal("corrupt controller record blocked offline recovery", err)
	}
	original, _ = os.ReadFile(output)
	if err := os.Rename(filepath.Join(profile, "logs"), filepath.Join(profile, "logs.saved")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "logs"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ExportDiagnostics(path, output); err == nil {
		t.Fatal("invalid log storage accepted")
	}
	got, _ := os.ReadFile(output)
	if !bytes.Equal(original, got) {
		t.Fatal("failed export overwrote destination")
	}
}
func TestTaskPanicRecordsFailureBeforeProcessExit(t *testing.T) {
	const flag = "CAGE_DIAGNOSTICS_PANIC_TEST"
	if profile := os.Getenv(flag); profile != "" {
		diagnostics.SetDefault(diagnostics.New(profile))
		m := new(jobManager)
		var operation sync.Mutex
		_, err := m.submit(&operation, "test", 0, nil, nil, func() error { panic("token=panic-private-secret task broke") })
		if err != nil {
			os.Exit(3)
		}
		select {}
	}
	profile := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestTaskPanicRecordsFailureBeforeProcessExit$")
	cmd.Env = append(os.Environ(), flag+"="+profile)
	if err := cmd.Run(); err == nil {
		t.Fatal("task panic unexpectedly kept process alive")
	}
	var bundle bytes.Buffer
	if err := diagnostics.New(profile).Export(&bundle, "test", nil, "offline"); err != nil {
		t.Fatal(err)
	}
	archive, _ := zip.NewReader(bytes.NewReader(bundle.Bytes()), int64(bundle.Len()))
	var messages string
	for _, f := range archive.File {
		r, _ := f.Open()
		b, _ := io.ReadAll(r)
		r.Close()
		messages += string(b)
	}
	if strings.Contains(messages, "panic-private-secret") || !strings.Contains(messages, "task broke") {
		t.Fatal("panic evidence missing or not redacted")
	}
}

func TestDiagnosticExportBusyAndManifest(t *testing.T) {
	s := testServer(t)
	s.diagnosticMu.Lock()
	w := request(s, "GET", "/api/diagnostics/export", "", "", true)
	s.diagnosticMu.Unlock()
	if w.Code != 409 {
		t.Fatal("parallel bundle generation allowed")
	}
	info := request(s, "GET", "/api/diagnostics", "", "", true)
	var v diagnostics.Info
	if json.Unmarshal(info.Body.Bytes(), &v) != nil || v.MaxBytes+v.GuestMaxBytes != 50<<20 || v.RetentionDays != 14 {
		t.Fatalf("policy unavailable: %s", info.Body.String())
	}
}
