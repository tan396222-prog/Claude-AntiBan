// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package qemu

import (
	"testing"

	"github.com/coreos/go-semver/semver"

	"github.com/lima-vm/lima/v2/pkg/limatype"
)

func TestWHPXQEMU64Features(t *testing.T) {
	for bits, want := range map[uint64]string{
		0:    "qemu64",
		0x60: "qemu64", // AMD SSE4a and XOP do not imply SSE4.2.
		0x0c: "qemu64,+ssse3,+sse4.1",
		0x9c: "qemu64,+ssse3,+sse4.1,+sse4.2,+popcnt",
	} {
		if got := whpxQEMU64Model(bits); got != want {
			t.Errorf("WHPX feature mask %#x: got %q, want %q", bits, got, want)
		}
	}
}

func TestWindowsCPUResolution(t *testing.T) {
	arch := limatype.X8664
	y := limatype.LimaYAML{Arch: &arch}
	// New and existing instances with no explicit CPU override both resolve
	// against the current host each time their QEMU command is generated.
	if got, want := resolveCPUType(&y, semver.New("11.1.0")), whpxQEMU64(); got != want {
		t.Fatalf("default for QEMU 11.1.0: got %q, want %q", got, want)
	}
	if got := resolveCPUType(&y, semver.New("11.1.1")); got != "host" {
		t.Fatalf("default for QEMU 11.1.1: got %q", got)
	}
	for _, override := range []string{"qemu64", "qemu64,+sse4.2", "Nehalem"} {
		y.VMOpts = limatype.VMOpts{limatype.QEMU: limatype.QEMUOpts{CPUType: limatype.CPUType{arch: override}}}
		if got := resolveCPUType(&y, semver.New("11.1.0")); got != override {
			t.Fatalf("explicit CPU type %q changed to %q", override, got)
		}
	}
}
