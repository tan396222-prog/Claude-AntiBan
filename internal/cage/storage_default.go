package cage

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// InitialStorageDirectory supplies a fresh packaged Windows installation's
// data location. Profiles and existing storage records keep their own paths.
func InitialStorageDirectory(exe string) string {
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return ""
	}
	return initialStorageDirectory(resolved, runtime.GOOS)
}

func initialStorageDirectory(exe, platform string) string {
	if platform != "windows" || !strings.EqualFold(filepath.Base(filepath.Dir(exe)), "resources") {
		return ""
	}
	return windowsApplicationDataDirectory(filepath.Dir(filepath.Dir(exe)), platform)
}

func windowsApplicationDataDirectory(app, platform string) string {
	if platform != "windows" {
		return ""
	}
	marker, err := os.ReadFile(filepath.Join(app, "Cage.install-id"))
	if err != nil || string(marker) != "io.github.lee-xeo.claude-cage" {
		return ""
	}
	if info, err := os.Stat(filepath.Join(app, "Cage.exe")); err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return filepath.Join(app, "CageData")
}
