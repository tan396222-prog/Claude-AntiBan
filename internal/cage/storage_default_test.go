package cage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsInitialDataDirectoryAndApplicationBoundary(t *testing.T) {
	profile, root := newStorageFixture(t)
	app := filepath.Join(root, "Cage")
	backend := filepath.Join(app, "resources", "cage.exe")
	writeStorageFixture(t, backend, []byte("backend"))
	writeStorageFixture(t, filepath.Join(app, "Cage.exe"), []byte("launcher"))
	if got := initialStorageDirectory(backend, "windows"); got != "" {
		t.Fatalf("unmarked build default=%s", got)
	}
	writeStorageFixture(t, filepath.Join(app, "Cage.install-id"), []byte("io.github.lee-xeo.claude-cage"))
	data := filepath.Join(app, "CageData")
	if got := initialStorageDirectory(backend, "windows"); got != data {
		t.Fatalf("default=%s want=%s", got, data)
	}
	for _, platform := range []string{"darwin", "linux"} {
		if got := initialStorageDirectory(backend, platform); got != "" {
			t.Fatalf("%s default changed to %s", platform, got)
		}
	}
	if err := validateStorageTarget(profile, data, app, "windows"); err != nil {
		t.Fatalf("reserved Windows data folder rejected: %v", err)
	}
	for _, target := range []string{app, filepath.Join(app, "resources", "data"), filepath.Join(app, "OtherData")} {
		if err := validateStorageTarget(profile, target, app, "windows"); err == nil {
			t.Fatalf("program boundary opened for %s", target)
		}
	}
	if err := validateStorageTarget(profile, data, app, "darwin"); err == nil {
		t.Fatal("Mac application directory allowed")
	}
	if err := os.Remove(filepath.Join(app, "Cage.install-id")); err != nil {
		t.Fatal(err)
	}
	if err := validateStorageTarget(profile, data, app, "windows"); err == nil {
		t.Fatal("unmarked installation allowed data inside app")
	}
}
