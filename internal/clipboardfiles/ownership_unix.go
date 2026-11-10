//go:build !windows

package clipboardfiles

import (
	"errors"
	"os"
	"syscall"
)

func removeCacheDirectory(root *os.Root, record cacheRecord) error {
	if err := root.Remove(record.Marker); err != nil {
		return err
	}
	if err := os.Remove(record.Directory); err != nil {
		return errors.Join(err, root.WriteFile(record.Marker, []byte(record.ID), 0600))
	}
	return nil
}

func lockCacheStore(file *os.File) (func(), error) {
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	return func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }, nil
}

func cacheOwnerAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
