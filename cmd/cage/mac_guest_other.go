//go:build !darwin || !arm64 || !cgo || no_vz

package main

import (
	"context"
	"errors"
)

func runMacGuest(context.Context, string, string, []string) error {
	return errors.New("macOS guests require an Apple silicon Mac with Virtualization.framework")
}

func macGuestLoginAvailable() bool { return false }
