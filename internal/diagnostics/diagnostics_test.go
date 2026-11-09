package diagnostics

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func readEvents(t *testing.T, s *Store) []Event {
	t.Helper()
	entries, err := s.files()
	if err != nil {
		t.Fatal(err)
	}
	var result []Event
	for _, entry := range entries {
		b, err := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range bytes.Split(bytes.TrimSpace(b), []byte("\n")) {
			if len(line) == 0 {
				continue
			}
			var e Event
			if err := json.Unmarshal(line, &e); err != nil {
				t.Fatalf("corrupt event: %s: %v", line, err)
			}
			result = append(result, e)
		}
	}
	return result
}
func TestStreamRedactsSplitSecretsAndPrivateKeys(t *testing.T) {
	s := New(t.TempDir())
	w := s.Stream("proxy", "error")
	input := "failure password=correct-horse token=split-secret https://user:pass@example.com/path?q=secret\nAuthorization: Bearer sensitive-token\nCookie: session=first-secret; custom=second-secret\n-----BEGIN PRIVATE KEY-----\nbase64-private-material\n-----END PRIVATE KEY-----\n/home/alice/private/project.txt C:\\Users\\bob\\project\\secret.txt me@example.com 203.0.113.8\nrecovered\n"
	for _, b := range []byte(input) {
		_, _ = w.Write([]byte{b})
	}
	_ = w.Close()
	var all string
	for _, e := range readEvents(t, s) {
		all += e.Message + "\n"
	}
	for _, secret := range []string{"correct-horse", "split-secret", "sensitive-token", "first-secret", "second-secret", "base64-private-material", "example.com", "alice", "bob", "203.0.113.8"} {
		if strings.Contains(all, secret) {
			t.Errorf("leaked %q: %s", secret, all)
		}
	}
	if !strings.Contains(all, "recovered") {
		t.Fatal("safe output lost")
	}
	long := s.Stream("task", "info")
	_, _ = long.Write([]byte(strings.Repeat("x", MaxMessage) + " token=too-long-secret\nnext line\n"))
	_ = long.Close()
	events := readEvents(t, s)
	if events[len(events)-1].Message != "next line" {
		t.Fatal("overlong line lost framing")
	}
}
func TestConcurrentRotationCapacityAndRetention(t *testing.T) {
	s := New(t.TempDir())
	s.fileBytes = 1000
	s.maxBytes = 3000
	now := time.Now()
	s.now = func() time.Time { return now }
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				if err := s.Record("error", "test", fmt.Sprintf("worker %d message %d %s", n, i, strings.Repeat("safe ", 20))); err != nil {
					t.Error(err)
				}
			}
		}(n)
	}
	wg.Wait()
	if len(readEvents(t, s)) == 0 {
		t.Fatal("no events")
	}
	info, err := s.Info()
	if err != nil || info.Bytes > s.maxBytes || info.Files < 2 {
		t.Fatalf("invalid rotation: %+v %v", info, err)
	}
	sentinel := filepath.Join(s.dir, "user-file.txt")
	_ = os.WriteFile(sentinel, []byte("keep"), 0600)
	files, _ := s.files()
	for _, f := range files {
		_ = os.Chtimes(filepath.Join(s.dir, f.Name()), now.Add(-15*24*time.Hour), now.Add(-15*24*time.Hour))
	}
	if err := s.Clean(); err != nil {
		t.Fatal(err)
	}
	info, _ = s.Info()
	if info.Bytes != 0 {
		t.Fatal("expired logs retained")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatal("unrelated file deleted")
	}
}
func TestExportWhitelistAndRedaction(t *testing.T) {
	profile := t.TempDir()
	s := New(profile)
	if err := s.Record("error", "test", "password=export-secret failure"); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(profile, "config.json"), []byte(`{"password":"must-not-export"}`), 0600)
	_ = os.WriteFile(filepath.Join(s.dir, "unknown.txt"), []byte("must-not-export"), 0600)
	var bundle bytes.Buffer
	if err := s.Export(&bundle, "test", []byte("token=guest-secret\nsafe guest"), "collected"); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(bundle.Bytes()), int64(bundle.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	var content string
	for _, f := range archive.File {
		r, _ := f.Open()
		b, _ := io.ReadAll(r)
		r.Close()
		names = append(names, f.Name)
		content += string(b)
	}
	for _, secret := range []string{"export-secret", "must-not-export", "guest-secret"} {
		if strings.Contains(content, secret) {
			t.Errorf("export leaked %s", secret)
		}
	}
	if !strings.Contains(content, "safe guest") || len(names) != 4 {
		t.Fatalf("missing evidence: %v", names)
	}
}
func TestRefusesSymlinkForWriteAndExport(t *testing.T) {
	profile := t.TempDir()
	s := New(profile)
	if err := s.Clean(); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "private.txt")
	_ = os.WriteFile(outside, []byte("outside-secret"), 0600)
	if err := os.Symlink(outside, filepath.Join(s.dir, "events.jsonl")); err != nil {
		t.Skip(err)
	}
	if err := s.Record("error", "test", "hello"); err == nil {
		t.Fatal("followed symlink")
	}
	var bundle bytes.Buffer
	if err := s.Export(&bundle, "test", nil, "offline"); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(bundle.Bytes(), []byte("outside-secret")) {
		t.Fatal("included symlink")
	}
	b, _ := os.ReadFile(outside)
	if string(b) != "outside-secret" {
		t.Fatal("modified external file")
	}
}
func TestGuestRunnerSharesRedactionAndExitStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("guest runner executes on Linux; POSIX shell fixture")
	}
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv unavailable")
	}
	// CI runs this fixture as an ordinary user; setup-uv's inherited cache
	// can belong to the root account that installed the build dependencies.
	t.Setenv("UV_CACHE_DIR", t.TempDir())
	script := filepath.Join(t.TempDir(), "runner.py")
	_ = os.WriteFile(script, []byte(GuestRunner()), 0600)
	cmd := exec.Command("uv", "run", "--no-project", "python", script, "/bin/sh", "-c", `printf 'Cookie: one=first-secret; two=second-secret\nhttps://user:pass@example.com/secret\nsafe-line\n'; exit 7`)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if err == nil {
		t.Fatal("exit status lost")
	} else if !strings.Contains(err.Error(), "exit status 7") {
		t.Fatalf("runner failed: %s %v %v", out, err, exit)
	}
	if strings.Contains(string(out), "first-secret") || strings.Contains(string(out), "second-secret") || strings.Contains(string(out), "example.com") {
		t.Fatalf("guest leaked: %s", out)
	}
	if !strings.Contains(string(out), "safe-line") {
		t.Fatalf("guest output missing: %s", out)
	}
}
func TestLegacyImportBoundsAndRemovesOldLogs(t *testing.T) {
	profile := t.TempDir()
	path := filepath.Join(profile, "control.log")
	_ = os.WriteFile(path, []byte(strings.Repeat("discard ", 50000)+"\npassword=old-secret\nsafe legacy\n"), 0600)
	s := New(profile)
	if err := s.ImportLegacy(profile); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("raw legacy log retained")
	}
	events := readEvents(t, s)
	if len(events) != 2 || strings.Contains(events[0].Message, "old-secret") {
		t.Fatalf("invalid migration: %+v", events)
	}
}

func TestConcurrentProcessesShareOneLog(t *testing.T) {
	const flag = "CAGE_DIAGNOSTICS_PROCESS_TEST"
	if profile := os.Getenv(flag); profile != "" {
		s := New(profile)
		for i := 0; i < 20; i++ {
			if err := s.Record("info", "worker", fmt.Sprintf("event %d", i)); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	profile := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var workers []*exec.Cmd
	for i := 0; i < 3; i++ {
		cmd := exec.Command(exe, "-test.run=^TestConcurrentProcessesShareOneLog$")
		cmd.Env = append(os.Environ(), flag+"="+profile)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		workers = append(workers, cmd)
	}
	for _, cmd := range workers {
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	if events := readEvents(t, New(profile)); len(events) != 60 {
		t.Fatalf("cross-process records lost: %d", len(events))
	}
}

func TestCrossDayRotatesSmallActiveFile(t *testing.T) {
	s := New(t.TempDir())
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	if err := s.Record("info", "test", "first day"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(24 * time.Hour)
	if err := s.Record("info", "test", "second day"); err != nil {
		t.Fatal(err)
	}
	files, err := s.files()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || len(readEvents(t, s)) != 2 {
		t.Fatal("small active file did not rotate across days")
	}
}
