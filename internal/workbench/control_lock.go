package workbench

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/sylvinus/agent-vm/internal/host"
)

// The lock contains no credentials. Old Windows versions created it with the
// Administrators group as owner. Test the OS lock before repairing its ACL;
// credential-file ownership must not be used as a process-liveness test.
func openControllerLock(profile string, flags int) (*os.File, error) {
	path := filepath.Join(profile, "control.lock")
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return nil, errors.New("控制锁必须是普通文件")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	f, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return nil, err
	}
	opened, statErr := f.Stat()
	current, pathErr := os.Lstat(path)
	if statErr != nil || pathErr != nil || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		f.Close()
		return nil, errors.New("控制锁路径已变化，请重试")
	}
	return f, nil
}

// A status probe can briefly own the lock while it checks or repairs its ACL.
// Let that probe finish before deciding that another controller owns the profile.
func lockControllerForStart(f *os.File) error {
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := lockController(f)
		if err == nil || !controllerLockContended(err) || !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func controllerLockStatus(profile string) (free bool, err error) {
	f, err := openControllerLock(profile, os.O_RDWR)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	defer f.Close()
	if err := lockController(f); err != nil {
		if controllerLockContended(err) {
			return false, nil
		}
		return false, err
	}
	defer unlockController(f)
	// Repair legacy ownership only while holding the same lock that protects
	// record publication. Never unlink a live lock or relax its permissions.
	if err := host.ProtectFile(f.Name()); err != nil {
		return false, err
	}
	if err := host.CheckPrivateFile(f.Name()); err != nil {
		return false, err
	}
	return true, nil
}

func controllerLockFree(profile string) bool {
	free, err := controllerLockStatus(profile)
	return free && err == nil
}
