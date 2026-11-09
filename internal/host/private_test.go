package host

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateStorageProtectsNewAndReplacedRecords(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profile")
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "credential")
	if err := os.WriteFile(path, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ProtectFile(path); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivateFile(path); err != nil {
		t.Fatal(err)
	}
	replacement, err := os.CreateTemp(dir, ".replacement-")
	if err != nil {
		t.Fatal(err)
	}
	if err := ProtectFile(replacement.Name()); err != nil {
		t.Fatal(err)
	}
	replacement.Close()
	if err := os.Rename(replacement.Name(), path); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivateFile(path); err != nil {
		t.Fatalf("atomic replacement lost private permissions: %v", err)
	}
	if err := CheckPrivateFile(dir); err == nil {
		t.Fatal("directory accepted as credential record")
	}
}
