package host

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ConfigureWindowsRuntime uses only the runtime shipped next to cage.exe.
// The install does not modify the user's system PATH or SSH configuration.
func ConfigureWindowsRuntime() error {
	// These describe an OpenSSH ancestor's private descriptors. Forwarding
	// them through a non-OpenSSH process corrupts a new SSH client's pipes.
	for _, name := range []string{"OPENSSH_STDIO_MODE", "c28fc6f98a2c44abbbd89d6a3037d0d9_POSIX_FD_STATE"} {
		os.Unsetenv(name)
	}
	version := windows.RtlGetVersion()
	if runtime.GOARCH != "amd64" || version.MajorVersion < 10 || version.BuildNumber < 19045 {
		return fmt.Errorf("Cage 需要 64 位 Windows 10 22H2（19045）或 Windows 11")
	}
	var processMachine, nativeMachine uint16
	if err := windows.IsWow64Process2(windows.CurrentProcess(), &processMachine, &nativeMachine); err != nil || nativeMachine != 0x8664 {
		return fmt.Errorf("Cage Windows 版本需要 Intel 或 AMD 的 x64 处理器，暂不支持 Windows on ARM")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	base := filepath.Join(filepath.Dir(exe), "runtime")
	paths := []string{}
	for _, item := range []struct{ dir, exe string }{{"qemu", "qemu-system-x86_64.exe"}, {"openssh", "ssh.exe"}} {
		dir := filepath.Join(base, item.dir)
		if st, err := os.Stat(filepath.Join(dir, item.exe)); err == nil && st.Mode().IsRegular() {
			paths = append(paths, dir)
			if item.dir == "qemu" {
				os.Setenv("AGENT_VM_QEMU_DIR", dir)
			} else {
				// Shellwords parses this override; forward slashes preserve paths
				// with spaces without interpreting Windows backslashes as escapes.
				os.Setenv("SSH", "'"+strings.ReplaceAll(filepath.ToSlash(filepath.Join(dir, "ssh.exe")), "'", "'\\''")+"'")
			}
		}
	}
	if len(paths) > 0 {
		paths = append(paths, os.Getenv("PATH"))
		return os.Setenv("PATH", strings.Join(paths, string(os.PathListSeparator)))
	}
	return nil
}

// This capability query does not require elevation and performs no changes.
// The desktop installer can offer the Windows Features screen separately.
func windowsHypervisorReady() error {
	proc := windows.NewLazySystemDLL("WinHvPlatform.dll").NewProc("WHvGetCapability")
	if err := proc.Find(); err != nil {
		return fmt.Errorf("请启用 Windows Hypervisor Platform（Windows 虚拟机监控程序平台）并重启后再启动环境")
	}
	var present, written uint32
	result, _, _ := proc.Call(0, uintptr(unsafe.Pointer(&present)), unsafe.Sizeof(present), uintptr(unsafe.Pointer(&written)))
	if result != 0 || written != 4 || present == 0 {
		return fmt.Errorf("Windows 虚拟化尚未就绪：请在固件中启用 CPU 虚拟化，启用 Windows Hypervisor Platform 并重启")
	}
	return nil
}
