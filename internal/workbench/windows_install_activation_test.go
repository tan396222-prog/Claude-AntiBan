package workbench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/guestos"
)

func TestWindowsInstallationActivationRejectsDifferentGuest(t *testing.T) {
	s := testServer(t)
	install := guestos.Installation{Version: 1, ID: uuid.New(), System: guestos.Windows11}
	state := filepath.Join(s.profile, "state")
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	state, err := cage.StoragePhysicalPath(state)
	if err != nil {
		t.Fatal(err)
	}
	binding := guestos.WindowsProfile{Version: 1, VMID: uuid.New(), GUIUserSID: "S-1-5-21-1-2-3-1001",
		InstanceDirectory: filepath.Join(state, "windows-"+strings.ReplaceAll(install.ID.String(), "-", "")),
		RelayExecutable:   filepath.Join(s.profile, "relay.exe"), PairingFile: filepath.Join(s.profile, "pairing.bin")}
	if err := guestos.BindWindowsProfile(s.profile, binding); err != nil {
		t.Fatal(err)
	}
	receipt := filepath.Join(s.profile, "windows-install-vm.txt")
	if err := os.WriteFile(receipt, []byte(binding.VMID.String()), 0600); err != nil {
		t.Fatal(err)
	}
	owner, err := s.installedWindowsVMOwner(install)
	if err != nil || owner != "windows/"+binding.VMID.String()+"/Running" {
		t.Fatalf("completed installation was not admitted: %q %v", owner, err)
	}
	if err := os.WriteFile(receipt, []byte(uuid.New().String()), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.installedWindowsVMOwner(install); err == nil {
		t.Fatal("another VM's receipt permitted controller activation")
	}
	if err := os.WriteFile(receipt, []byte(binding.VMID.String()), 0600); err != nil {
		t.Fatal(err)
	}
	install.ID = uuid.New()
	if _, err := s.installedWindowsVMOwner(install); err == nil {
		t.Fatal("another installation's disk permitted controller activation")
	}
}
