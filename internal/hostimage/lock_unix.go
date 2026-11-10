//go:build !windows

package hostimage

import (
	"os"

	"golang.org/x/sys/unix"
)

func lockDownload(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}
