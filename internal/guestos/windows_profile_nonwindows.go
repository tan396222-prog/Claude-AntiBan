//go:build !windows

package guestos

// Non-Windows tests can validate the public binding without interpreting NTFS.
func protectWindowsProfile(string) error { return nil }
