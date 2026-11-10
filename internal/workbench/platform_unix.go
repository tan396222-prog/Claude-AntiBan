//go:build !windows

package workbench

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"syscall"
)

func lockController(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}
func unlockController(f *os.File) error      { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
func controllerLockContended(err error) bool { return errors.Is(err, syscall.EWOULDBLOCK) }
func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err != syscall.ESRCH
}
func terminateLegacy(pid int) error  { return syscall.Kill(pid, syscall.SIGTERM) }
func detachController(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
func sshExecutable() string          { return "/usr/bin/ssh" }
func sshEnvironment() []string {
	return []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=C", "LC_ALL=C", "TERM=xterm-256color"}
}
func defaultKeyboard() string {
	if runtime.GOOS == "darwin" {
		return "mac"
	}
	return "native"
}
