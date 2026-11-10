// Package guestos checks host prerequisites for a local guest system.
package guestos

import (
	"os"
	"path/filepath"
)

// Guest system IDs are shared with the workbench installer.
const (
	Debian13  = "debian-13"
	MacOS     = "macos"
	Windows11 = "windows-11"
)

// MacRestore comes from Apple's compatibility catalog for this particular host.
type MacRestore struct {
	Version            string `json:"version"`
	Build              string `json:"build"`
	URL                string `json:"url"`
	MinimumCPUs        int    `json:"minimumCPUs"`
	MinimumMemoryBytes uint64 `json:"minimumMemoryBytes"`
}

// Inspection keeps host capability separate from availability in the workbench.
type Inspection struct {
	Guest            string      `json:"guest"`
	HostCheckPassed  bool        `json:"hostCheckPassed"`
	WorkbenchSupport string      `json:"workbenchSupport"`
	Detail           string      `json:"detail"`
	Restore          *MacRestore `json:"restore,omitempty"`
}

// HasNativeWorkbenchKit checks the resources shipped alongside this backend.
// A standalone diagnostic binary can inspect a host without the desktop kit.
func HasNativeWorkbenchKit(executable, system string) bool {
	var files []string
	switch system {
	case MacOS:
		files = []string{"prepare-macos-media.sh", "provision-macos-workbench.sh", "project/cage-sshfs", "native-browser/macos-native-browser.mjs", "cage-finish-initial-setup"}
	case Windows11:
		files = []string{"new-windows-guest.ps1", "setup-windows-guest.ps1", "windows-host-image.ps1", "complete-windows-guest.ps1", "install-windows-host-manager.ps1", "payload/Cage.HyperVManager.exe", "payload/Cage.GuestClipboard.exe"}
	default:
		return false
	}
	for _, name := range files {
		info, err := os.Stat(filepath.Join(filepath.Dir(executable), "guest-os", name))
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return true
}

func nativeWorkbenchSupport(system string, hostReady bool) string {
	if !hostReady {
		return "host-not-ready"
	}
	exe, err := os.Executable()
	if err != nil || !HasNativeWorkbenchKit(exe, system) {
		return "installation-kit-missing"
	}
	return "desktop-installer"
}
