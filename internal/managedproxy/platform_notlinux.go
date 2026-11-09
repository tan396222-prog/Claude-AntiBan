//go:build !linux

package managedproxy

import (
	"context"
	"errors"
)

func startLinuxCore(context.Context, string, Store) error {
	return errors.New("Linux core is unavailable on this platform")
}
func stopLinuxCore(string) error { return errors.New("Linux core is unavailable on this platform") }
func linuxPhysicalInterface(context.Context) (string, error) {
	return "", errors.New("Linux interface is unavailable on this platform")
}
