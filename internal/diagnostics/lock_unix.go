//go:build !windows

package diagnostics

import (
	"golang.org/x/sys/unix"
	"os"
)

func lock(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX) }
func unlock(f *os.File)     { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }
