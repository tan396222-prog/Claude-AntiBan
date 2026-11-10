//go:build !windows

package guestos

import (
	"os"
	"testing"
)

func createWindowsStorageFixtureDisk(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("synthetic persistent disk data"), 0600); err != nil {
		t.Fatal(err)
	}
}
