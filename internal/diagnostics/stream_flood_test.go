package diagnostics

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestStreamDrainsWarningFlood(t *testing.T) {
	const flag = "CAGE_DIAGNOSTICS_FLOOD"
	const count = 200000
	const warning = "W: Tried to start delayed item package, but failed\n"
	if os.Getenv(flag) == "1" {
		chunk := strings.Repeat(warning, 1000)
		for i := 0; i < count/1000; i++ {
			fmt.Fprint(os.Stderr, chunk)
		}
		fmt.Fprintln(os.Stderr, "installation completed")
		return
	}
	s := New(t.TempDir())
	var visible strings.Builder
	w := NewStream(s, "setup", "info", func(line string) { visible.WriteString(line) })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestStreamDrainsWarningFlood$")
	cmd.Env = append(os.Environ(), flag+"=1")
	cmd.Stderr = w
	start := time.Now()
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	w.Close()
	events := readEvents(t, s)
	total := uint64(1)
	for _, event := range events {
		var repeats uint64
		if _, err := fmt.Sscanf(event.Message, "[previous output repeated %d additional times]", &repeats); err == nil {
			total += repeats
		}
	}
	if total != count || len(events) > 25 || !strings.Contains(visible.String(), "installation completed") {
		t.Fatalf("flood not coalesced or final output lost: count=%d events=%d", total, len(events))
	}
	if w.batchBytes != 0 || len(w.line) != 0 || len(visible.String()) > 4096 {
		t.Fatal("output retained after drain")
	}
	t.Logf("drained %d warnings (%d bytes) in %s, %d events", count, count*len(warning), time.Since(start), len(events))
}

func TestStreamCoalescingKeepsProgressAndDistinctSecrets(t *testing.T) {
	s := New(t.TempDir())
	w := s.Stream("setup", "info")
	input := "password=one\npassword=two\nCAGE_TOOL_BYTES pkg 0 10\nCAGE_TOOL_BYTES pkg 0 10\nlast\nlast\n"
	w.Write([]byte(input))
	w.Close()
	events := readEvents(t, s)
	if len(events) != 6 || !strings.Contains(events[5].Message, "1 additional times") {
		t.Fatalf("unexpected events: %+v", events)
	}
	for _, e := range events {
		if strings.Contains(e.Message, "one") || strings.Contains(e.Message, "two") {
			t.Fatal("secret leaked")
		}
	}
}

func TestStreamBatchRotationPreservesEvents(t *testing.T) {
	s := New(t.TempDir())
	s.fileBytes = 1200
	s.maxBytes = 100000
	w := s.Stream("setup", "info")
	var input strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&input, "package %d installed\n", i)
	}
	w.Write([]byte(input.String()))
	w.Close()
	events := readEvents(t, s)
	if len(events) != 200 {
		t.Fatalf("batch rotation lost events: %d", len(events))
	}
	files, _ := s.files()
	for _, f := range files {
		if f.Size() > s.fileBytes {
			t.Fatalf("file exceeded rotation bound: %s %d", f.Name(), f.Size())
		}
	}
}
