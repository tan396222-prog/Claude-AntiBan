// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package qemu

import (
	"path/filepath"
	"unicode/utf8"
)

// The Windows QEMU build cannot reliably open a non-ASCII pflash argument.
// CreateProcessW takes the working directory as UTF-16, so QEMU can open
// the original firmware with its ASCII basename without making a stale copy.
func windowsFirmwareArgument(path string) (argument, workingDir string) {
	dir := filepath.Dir(path)
	for _, r := range dir {
		if r >= utf8.RuneSelf && filepath.IsAbs(path) {
			return filepath.Base(path), dir
		}
	}
	return path, ""
}
