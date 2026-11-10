package vm

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sylvinus/agent-vm/internal/diagnostics"
)

func TestVMStartErrorPreservesQEMUFailureAndRedactsSecrets(t *testing.T) {
	previous := diagnostics.Default()
	store := diagnostics.New(t.TempDir())
	diagnostics.SetDefault(store)
	t.Cleanup(func() { diagnostics.SetDefault(previous) })
	dir := t.TempDir()
	log := strings.Repeat("{\"msg\":\"[qemu[stderr]] warning: host doesn't support requested feature\"}\n", 12) + `{"msg":"[qemu[stderr]] WHPX: Failed to enable nested virtualization, hr=80370302"}
{"msg":"[qemu[stderr]] failed to initialize whpx: Invalid argument token=private-test-value"}
panic: send on closed channel
`
	if err := os.WriteFile(filepath.Join(dir, "ha.stderr.log"), []byte(log), 0600); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("host agent process has exited")
	err := vmStartError(cause, dir, time.Time{})
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "80370302") || strings.Contains(err.Error(), "private-test-value") || strings.Contains(err.Error(), "panic:") {
		t.Fatalf("bad startup error: %v", err)
	}
	var bundle bytes.Buffer
	if err := store.Export(&bundle, "test", nil, "guest never booted"); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(bundle.Bytes()), int64(bundle.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var exported strings.Builder
	for _, file := range archive.File {
		if !strings.HasPrefix(file.Name, "logs/") {
			continue
		}
		r, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		exported.Write(data)
	}
	if text := exported.String(); !strings.Contains(text, "80370302") || !strings.Contains(text, "panic:") || strings.Contains(text, "private-test-value") {
		t.Fatalf("diagnostic export lost details or exposed credentials: %s", text)
	}
	if vmStartError(context.Canceled, dir, time.Time{}) != context.Canceled {
		t.Fatal("cancellation replaced by an old QEMU error")
	}
	if vmStartError(cause, dir, time.Now().Add(time.Second)) != cause {
		t.Fatal("an earlier attempt's log replaced this failure")
	}
}
