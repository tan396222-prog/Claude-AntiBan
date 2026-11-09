// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package qemu

import (
	"unsafe"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/windows"
)

func whpxQEMU64() string {
	// Query the processor features available to Windows Hypervisor Platform
	// guests. A nested hypervisor may expose a subset of the host's CPUID.
	query := windows.NewLazySystemDLL("WinHvPlatform.dll").NewProc("WHvGetCapability")
	if err := query.Find(); err != nil {
		logrus.WithError(err).Warn("Cannot query WHPX processor features; using qemu64 defaults")
		return "qemu64"
	}
	const processorFeatures = 0x1001 // WHvCapabilityCodeProcessorFeatures
	var bits uint64
	var written uint32
	status, _, _ := query.Call(processorFeatures, uintptr(unsafe.Pointer(&bits)),
		unsafe.Sizeof(bits), uintptr(unsafe.Pointer(&written)))
	if int32(status) < 0 || uintptr(written) != unsafe.Sizeof(bits) {
		logrus.Warnf("Cannot query WHPX processor features (HRESULT %#x, %d bytes); using qemu64 defaults", uint32(status), written)
		return "qemu64"
	}
	return whpxQEMU64Model(bits)
}

// Bit positions are from the Windows SDK's WHV_X64_PROCESSOR_FEATURES.
func whpxQEMU64Model(bits uint64) string {
	model := "qemu64"
	for _, feature := range []struct {
		mask uint64
		flag string
	}{
		{1 << 2, "+ssse3"},
		{1 << 3, "+sse4.1"},
		{1 << 4, "+sse4.2"},
		{1 << 7, "+popcnt"},
	} {
		if bits&feature.mask != 0 {
			model += "," + feature.flag
		}
	}
	return model
}
