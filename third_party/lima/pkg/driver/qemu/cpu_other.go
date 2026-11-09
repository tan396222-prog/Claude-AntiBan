// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

//go:build !windows || !amd64

package qemu

// Only the Windows amd64 driver defaults to qemu64.
func whpxQEMU64() string { return "qemu64" }
