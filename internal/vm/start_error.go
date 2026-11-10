package vm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sylvinus/agent-vm/internal/diagnostics"
)

// Keep the first QEMU stderr messages from this failed start visible through
// the CLI and desktop job, rather than only the later hostagent exit error.
func vmStartError(cause error, instanceDir string, since time.Time) error {
	if cause == nil || errors.Is(cause, context.Canceled) {
		return cause
	}
	f, err := os.Open(filepath.Join(instanceDir, "ha.stderr.log"))
	if err != nil {
		return cause
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.ModTime().Before(since) {
		return cause
	}
	if info.Size() > 64<<10 {
		_, _ = f.Seek(info.Size()-(64<<10), io.SeekStart)
	}
	b, err := io.ReadAll(io.LimitReader(f, 64<<10))
	if err != nil {
		return cause
	}
	if info.Size() > 64<<10 {
		// Never export a partial record cut through a credential or private key.
		if next := strings.IndexByte(string(b), '\n'); next >= 0 {
			b = b[next+1:]
		} else {
			b = nil
		}
	}
	// Keep the bounded hostagent log available in the existing diagnostic
	// export, including failures which happened before the guest could boot.
	stream := diagnostics.NewStream(diagnostics.Default(), "vm/start", "error", nil)
	_, _ = stream.Write(b)
	_ = stream.Close()
	var messages []string
	for _, line := range strings.Split(string(b), "\n") {
		var record struct {
			Message string `json:"msg"`
		}
		if json.Unmarshal([]byte(line), &record) == nil && record.Message != "" {
			line = record.Message
		}
		_, message, ok := strings.Cut(line, "qemu[stderr]")
		if !ok {
			continue
		}
		message = strings.TrimSpace(strings.TrimLeft(message, "]: \""))
		if message == "" || strings.Contains(strings.ToLower(message), "warning:") {
			continue
		}
		messages = append(messages, diagnostics.Redact(message))
		if len(messages) == 6 {
			break
		}
	}
	if len(messages) == 0 {
		return cause
	}
	summary := strings.Join(messages, "；")
	if len(summary) > 2048 {
		summary = strings.ToValidUTF8(summary[:2048], "")
	}
	return fmt.Errorf("QEMU 启动失败：%s；%w", summary, cause)
}
