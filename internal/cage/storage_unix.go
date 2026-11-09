//go:build !windows

package cage

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func lockStorageFile(f *os.File, exclusive bool) (bool, error) {
	mode := unix.LOCK_SH
	if exclusive {
		mode = unix.LOCK_EX
	}
	err := unix.Flock(int(f.Fd()), mode|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}
func linkStorage(link, target string) error { return os.Symlink(target, link) }
func sparseStorageFile(*os.File) error      { return nil }

func storageMountRoot(path string) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return false, err
	}
	return fi.Sys().(*syscall.Stat_t).Dev != parent.Sys().(*syscall.Stat_t).Dev, nil
}

func syncStorageDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func renameStorage(from, to string) error {
	if err := os.Rename(from, to); err != nil {
		return err
	}
	return syncStorageDir(filepath.Dir(to))
}
