package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/guestos"
)

func TestProxyChangeClosesBoundWindowsGate(t *testing.T) {
	profile := t.TempDir()
	instance := t.TempDir()
	binding := guestos.WindowsProfile{Version: 1, VMID: uuid.New(), GUIUserSID: "S-1-5-21-1-2-3-1001", InstanceDirectory: instance, RelayExecutable: filepath.Join(profile, "relay.exe"), PairingFile: filepath.Join(profile, "pairing.bin")}
	if err := guestos.BindWindowsProfile(profile, binding); err != nil {
		t.Fatal(err)
	}
	gate, err := cage.NewEgressGate(cage.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	stop, err := cage.StartGateControl(filepath.Join(profile, "config.json"), instance, gate)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if err := closeManagedGates(profile); err != nil {
		t.Fatal(err)
	}
	if gate.Status().Mode != cage.GateClosed || gate.Status().Reason != "代理核心停止或重启，等待重新检测" {
		t.Fatal("CLI proxy lifecycle missed the bound Windows gate")
	}
}

func TestProxyChangeRejectsMalformedWindowsBinding(t *testing.T) {
	profile := t.TempDir()
	if err := os.WriteFile(guestos.WindowsProfilePath(profile), []byte(`{"version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := closeManagedGates(profile); err == nil {
		t.Fatal("proxy mutation ignored an invalid native guest binding")
	}
}
