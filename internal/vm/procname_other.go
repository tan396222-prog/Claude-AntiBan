//go:build !windows

package vm

func isWindowsQEMU(int, string, string) bool { return false }
