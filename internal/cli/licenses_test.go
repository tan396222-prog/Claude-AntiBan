package cli

import (
	"strings"
	"testing"

	"github.com/sylvinus/agent-vm/internal/vm/vmtest"
)

func TestLicenses(t *testing.T) {
	te := newTestEnv(t, vmtest.New())
	if code := te.run("licenses"); code != 0 || te.stderr.Len() != 0 {
		t.Fatalf("licenses: %d\n%s", code, te.out())
	}
	out := te.stdout.String()
	// agent-vm's own license, the notice, and each project built into the
	// binary: Apache-2.0 (with Lima's NOTICE), BSD-2 and BSD-3 ones.
	for _, want := range []string{
		"MIT License", "Copyright (c) 2026 Sylvain Zimmer",
		"third_party/ holds", "section 4(b)",
		"github.com/lima-vm/lima/v2 ./third_party/lima: LICENSE",
		"github.com/lima-vm/lima/v2 ./third_party/lima: NOTICE",
		"github.com/lima-vm/sshocker ./third_party/sshocker: LICENSE",
		"github.com/containers/gvisor-tap-vsock ./third_party/gvisor-tap-vsock: LICENSE",
		"github.com/pkg/sftp ./third_party/sftp: LICENSE",
		"golang.org/x/sys ",
		"Go standard library",
		"Apache License",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the output lacks %q", want)
		}
	}
	if te.run("licenses", "x") != 2 || !strings.Contains(te.out(), "Usage: agent-vm licenses") {
		t.Errorf("an argument: %s", te.out())
	}
	if te.run("--readonly", "licenses") != exitUsage {
		t.Errorf("a VM option: %s", te.out())
	}
}
