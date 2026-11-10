// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package qemu

import "testing"

func TestWHPXMachineCompatibility(t *testing.T) {
	for _, accel := range []string{"whpx", "kvm", "hvf", "nvmm"} {
		want := "q35,accel=" + accel
		if accel == "whpx" {
			want += ",kernel-irqchip=off"
		}
		args := appendQ35Machine(nil, accel)
		if got, _ := argValue(args, "-machine"); got != want {
			t.Fatalf("%s: got %q, want %q", accel, got, want)
		}
	}
	// Retain Lima's explicit override contract for custom QEMU arguments.
	args := appendQ35Machine([]string{"-machine", "q35,accel=whpx,kernel-irqchip=on"}, "whpx")
	if len(args) != 2 || args[1] != "q35,accel=whpx,kernel-irqchip=on" {
		t.Fatalf("overrode explicit machine: %v", args)
	}
}
