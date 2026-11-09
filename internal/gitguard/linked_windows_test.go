package gitguard

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsMetadataHardLinkIsDetected(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config")
	if err := os.WriteFile(config, []byte("metadata"), 0600); err != nil {
		t.Fatal(err)
	}
	if linked(config) {
		t.Fatal("single link reported as shared metadata")
	}
	if err := os.Link(config, filepath.Join(dir, "editable-alias")); err != nil {
		t.Fatal(err)
	}
	if !linked(config) {
		t.Fatal("writable NTFS alias of metadata was not detected")
	}
}
