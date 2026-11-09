// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package qemu

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/lima-vm/lima/v2/pkg/limatype"
)

func TestWindowsFirmwareArgument(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "测试安装, Cage", "share")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	const name = "edk2-x86_64-code.fd"
	path := filepath.Join(dir, name)
	arg, cwd := windowsFirmwareArgument(path)
	if arg != name || cwd != dir {
		t.Fatalf("got argument %q and working directory %q", arg, cwd)
	}
	// Replacing the packaged firmware must also replace what QEMU reads.
	for _, revision := range []string{"first release", "updated release"} {
		if err := os.WriteFile(path, []byte(revision), 0o600); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(cwd, arg))
		if err != nil || string(data) != revision {
			t.Fatalf("reading %q: got %q, %v", revision, data, err)
		}
	}
}

func TestWindowsPackagedFirmwareBeatsOldUserCopy(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows QEMU distribution layout")
	}
	root := t.TempDir()
	home := filepath.Join(root, "用户")
	t.Setenv("USERPROFILE", home)
	binDir := filepath.Join(root, "安装目录", "qemu")
	const name = "edk2-x86_64-code.fd"
	bundled := filepath.Join(binDir, "share", name)
	for _, path := range []string{filepath.Join(home, ".local", "share", "qemu", name), bundled} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(path), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := getFirmware(filepath.Join(binDir, "qemu-system-x86_64.exe"), limatype.X8664)
	if err != nil || got != bundled {
		t.Fatalf("selected %q instead of the packaged %q: %v", got, bundled, err)
	}
}
