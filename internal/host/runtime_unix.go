//go:build !windows

package host

import "os/exec"

func HideCommandWindow(cmd *exec.Cmd) {}

func ConfigureWindowsRuntime() error { return nil }
func windowsHypervisorReady() error  { return nil }
