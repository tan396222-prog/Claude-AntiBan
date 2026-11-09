//go:build !windows

package host

import (
	"errors"
	"os"
)

func protectPrivate(path string, dir bool) error {
	mode := os.FileMode(0600)
	if dir {
		mode = 0700
	}
	return os.Chmod(path, mode)
}

func checkPrivate(_ string, info os.FileInfo) error {
	if info.Mode().Perm()&0077 != 0 {
		return errors.New("private record is accessible to other users")
	}
	return nil
}
