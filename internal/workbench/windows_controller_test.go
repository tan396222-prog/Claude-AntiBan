package workbench

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/guestos"
)

func TestWindowsControllerCannotTreatUnknownVMAsIdle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native Hyper-V state is covered by the opt-in Windows E2E")
	}
	profile := t.TempDir()
	binding := guestos.WindowsProfile{Version: 1, VMID: uuid.New(), GUIUserSID: "S-1-5-21-1-2-3-1001", InstanceDirectory: filepath.Join(profile, "hyperv"), RelayExecutable: filepath.Join(profile, "relay.exe"), PairingFile: filepath.Join(profile, "pairing.bin")}
	if err := guestos.BindWindowsProfile(profile, binding); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(profile, "state", "lima", "unrelated")
	if err := os.MkdirAll(legacy, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writePrivate(filepath.Join(legacy, "cage-running.json"), map[string]int{"pid": os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	if _, err := controllerVMOwners(profile); err == nil {
		t.Fatal("unsupported Hyper-V probe became an idle or unrelated Lima workload")
	}
}
