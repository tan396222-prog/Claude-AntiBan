//go:build !windows

package clipboardfiles

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestCacheReleasePreservesAnAlreadyOpenExternalReader(t *testing.T) {
	store, selection := cacheFixture(t)
	reader, err := os.Open(selection.Paths[0])
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err := store.Release(selection.ID); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	if err != nil || string(data) != "complete copied bytes" {
		t.Fatal("the open external reader lost its bytes", err)
	}
	if _, err := os.Open(selection.Paths[0]); !os.IsNotExist(err) {
		t.Fatal("a retired path remained available to a new reader")
	}
}

func TestCacheReleaseRetainsMarkerAfterDirectoryRemovalFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can remove the protected test directory")
	}
	store, selection := cacheFixture(t)
	parent := filepath.Dir(filepath.Dir(selection.Paths[0]))
	if err := os.Chmod(parent, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(parent, 0700)
	if err := store.Release(selection.ID); err == nil {
		t.Fatal("a protected directory was removed")
	}
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if err := store.Release(selection.ID); err != nil {
		t.Fatal("the failed removal lost its creation proof", err)
	}
}
