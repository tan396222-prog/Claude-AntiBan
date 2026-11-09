//go:build !windows

package host

func ConfigureWindowsRuntime() error { return nil }
func windowsHypervisorReady() error  { return nil }
