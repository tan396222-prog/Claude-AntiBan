package main

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

func TestGuestOSVMIDRequiresExplicitValidTarget(t *testing.T) {
	const valid = "ad2d0746-c50c-4dbd-a5f3-495ccfa4cbc2"
	for _, args := range [][]string{
		{"--json", "--vm-id", "this is not a GUID"},
		{"--json", "--vm-id", "00000000-0000-0000-0000-000000000000"},
		{"--vm-id", valid},
		{"--json", "--vm-id", valid, "--vm-id", valid},
	} {
		var out bytes.Buffer
		if err := writeGuestOSInspection(t.Context(), args, &out); err == nil || out.Len() != 0 {
			t.Fatalf("unexpected guest inspection for %v: %v, %q", args, err, out.String())
		}
	}
	if runtime.GOOS != "windows" {
		var out bytes.Buffer
		err := writeGuestOSInspection(t.Context(), []string{"--json", "--vm-id", valid}, &out)
		if err == nil || !strings.Contains(err.Error(), "Windows") || out.Len() != 0 {
			t.Fatalf("unexpected non-Windows lookup: %v, %q", err, out.String())
		}
	}
}
