// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package qemu

func appendQ35Machine(args []string, accel string) []string {
	machine := "q35,accel=" + accel
	if accel == "whpx" {
		// QEMU 11 can fail to enable nested virtualization on WHPX even
		// when Windows reports it as supported (HRESULT 80370302). Use
		// QEMU's interrupt controller emulation for the Cage Linux guest.
		machine += ",kernel-irqchip=off"
	}
	return appendArgsIfNoConflict(args, "-machine", machine)
}
