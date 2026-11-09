//go:build !windows

package managedproxy

import (
	"context"
	"errors"
)

func coreEnvironment(dir string) []string { return []string{"PATH=/usr/bin:/bin", "HOME=" + dir} }
func startWindowsCore(context.Context, string, Store) error {
	return errors.New("Windows core is unavailable on this platform")
}
func stopWindowsCore(string) error { return errors.New("Windows core is unavailable on this platform") }
func windowsPhysicalInterface(context.Context) (string, error) {
	return "", errors.New("Windows interface is unavailable on this platform")
}
