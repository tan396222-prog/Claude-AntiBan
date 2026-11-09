package workbench

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

func lockController(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, new(windows.Overlapped))
}
func unlockController(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}
func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return !errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	defer windows.CloseHandle(h)
	var code uint32
	return windows.GetExitCodeProcess(h, &code) != nil || code == 259
}
func terminateLegacy(pid int) error {
	return errors.New("Windows 不支持接管未验证的旧版后台")
}
func detachController(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS}
}
func hideCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
func sshExecutable() string {
	exe, _ := os.Executable()
	bundled := filepath.Join(filepath.Dir(exe), "runtime", "openssh", "ssh.exe")
	if st, err := os.Stat(bundled); err == nil && st.Mode().IsRegular() {
		return bundled
	}
	// Source tests and standalone developer builds can use Windows' official
	// optional client. Do not search a project-controlled PATH for ssh.exe.
	return filepath.Join(os.Getenv("SystemRoot"), "System32", "OpenSSH", "ssh.exe")
}
func sshEnvironment() []string {
	// OpenSSH needs Windows system directories but must not inherit keys,
	// SSH_AUTH_SOCK, proxy settings or user-supplied SSH configuration.
	// Win32-OpenSSH requires ProgramData before it initializes stderr.
	return []string{"SystemRoot=" + os.Getenv("SystemRoot"), "WINDIR=" + os.Getenv("WINDIR"), "ProgramData=" + os.Getenv("ProgramData"), "TEMP=" + os.TempDir(), "TMP=" + os.TempDir(), "PATH=" + filepath.Dir(sshExecutable()), "LANG=C", "LC_ALL=C", "TERM=xterm-256color"}
}
func defaultKeyboard() string { return "native" }
