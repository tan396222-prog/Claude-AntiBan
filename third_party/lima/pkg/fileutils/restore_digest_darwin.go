// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package fileutils

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"

	"github.com/opencontainers/go-digest"
)

// RestoreImageDigest hashes one regular file and checks that the same version is
// still selected at its pathname. selected may precede Apple's compatibility check.
func RestoreImageDigest(ctx context.Context, path string, selected os.FileInfo) (digest.Digest, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !before.Mode().IsRegular() {
		return "", errors.New("macOS restore image must be a regular file")
	}
	if selected != nil && !sameRestoreImage(selected, before) {
		return "", fmt.Errorf("macOS restore image changed before hashing: %q", path)
	}
	d := digest.SHA256.Digester()
	buf := make([]byte, 1024*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(buf)
		_, _ = d.Hash().Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	after, err := os.Lstat(path)
	if err != nil || !sameRestoreImage(before, after) {
		return "", fmt.Errorf("macOS restore image changed during hashing: %q", path)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return d.Digest(), nil
}

func sameRestoreImage(a, b os.FileInfo) bool {
	// ctime detects an in-place rewrite even when the owner restores mtime.
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime()) &&
		a.Sys().(*syscall.Stat_t).Ctimespec == b.Sys().(*syscall.Stat_t).Ctimespec
}
