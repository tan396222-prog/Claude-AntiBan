package workbench

import (
	"sync"
	"testing"
)

// Child pipes may split a progress line across writes. A fallback mirror may
// restart from zero, and an absent Content-Length must stay indeterminate.
func TestSetupProgressFromChildOutput(t *testing.T) {
	var m jobManager
	var op sync.Mutex
	var points []*SetupProgress
	_, err := m.submit(&op, "start", 0, nil, nil, func() error {
		e := m.execution()
		e.setSetupProgress(&SetupProgress{Phase: "image"})
		for _, part := range []string{"CAGE_IMAGE_BYTES 25", " 100\n", "CAGE_IMAGE_BYTES 0 -1\n", "CAGE_IMAGE_BYTES 123 5\n", "CAGE_IMAGE_READY\n", "Installing Claude Code...\n"} {
			_, _ = e.Write([]byte(part))
			points = append(points, m.snapshot().SetupProgress)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	awaitJobIdle(t, &m)
	if len(points) != 6 || points[0].Received != nil || points[1].Received == nil || *points[1].Received != 25 || points[1].Total != 100 || points[2].Received == nil || *points[2].Received != 0 || points[2].Total != 0 || points[3] != points[2] || points[4].Received != nil || points[5].Phase != "tools" {
		t.Fatalf("bad setup progress sequence: %+v", points)
	}
}

func TestToolProgressUsesOnlyKnownPackageNames(t *testing.T) {
	p := parseSetupProgress("CAGE_TOOL_BYTES claude-code-linux-x64.tgz 4980736")
	if p == nil || p.Phase != "tools" || p.Received == nil || *p.Received != 4980736 || p.Total != 0 {
		t.Fatalf("expected current bytes without a guessed total, got %+v", p)
	}
	if p := parseSetupProgress("CAGE_TOOL_BYTES untrusted 12"); p != nil {
		t.Fatalf("unexpected package name entered UI: %+v", p)
	}
	if p := parseSetupProgress("CAGE_TOOL_BYTES chromium-common.deb 5 10"); p == nil || p.Total != 10 || *p.Received != 5 {
		t.Fatalf("missing Chromium progress from signed index: %+v", p)
	}
	if p := parseSetupProgress("CAGE_TOOL_BYTES chromium.deb 12 10"); p != nil {
		t.Fatalf("excess bytes entered UI: %+v", p)
	}
}
