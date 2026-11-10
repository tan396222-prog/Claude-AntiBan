package guestos

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func windowsBindingFixture(root string) WindowsProfile {
	return WindowsProfile{Version: 1, VMID: uuid.MustParse("2a6f8208-3906-4e16-8233-2b9d28ecf7be"), GUIUserSID: "S-1-5-21-123-456-789-1001", GUIUser: "Guest", InstanceDirectory: filepath.Join(root, "vm"), RelayExecutable: filepath.Join(root, "agent.exe"), PairingFile: filepath.Join(root, "pairing.dpapi")}
}

func TestWindowsBindingPreservesIdentityAndHasNoSecretArgument(t *testing.T) {
	root := t.TempDir()
	binding := windowsBindingFixture(root)
	if err := BindWindowsProfile(root, binding); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadWindowsProfile(root)
	if err != nil || *loaded != binding {
		t.Fatalf("binding changed: %v", err)
	}
	cmd, err := WindowsRelayCommand(context.Background(), root, "export", "--paths-stdin")
	if err != nil {
		t.Fatal(err)
	}
	if len(cmd.Args) != 5 || cmd.Args[1] != "--relay-profile" || cmd.Args[2] != WindowsProfilePath(root) || cmd.Args[3] != "export" || cmd.Args[4] != "--paths-stdin" {
		t.Fatal("relay command did not preserve the profile boundary")
	}
	different := binding
	different.GUIUserSID = "S-1-5-21-123-456-789-1002"
	if err := BindWindowsProfile(root, different); err == nil {
		t.Fatal("another GUI user replaced the binding")
	}
	if _, err := WindowsRelayCommand(context.Background(), root, "export", `["C:\\private"]`); err == nil {
		t.Fatal("paths accepted in argv")
	}
}

func TestWindowsBindingRejectsUnknownFieldsAndUnsafeIdentity(t *testing.T) {
	for _, kind := range []string{"system", "empty-vm", "relative", "relative-network", "unknown", "trailing"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			binding := windowsBindingFixture(root)
			switch kind {
			case "system":
				binding.GUIUserSID = "S-1-5-18"
			case "empty-vm":
				binding.VMID = uuid.Nil
			case "relative":
				binding.PairingFile = "relative"
			case "relative-network":
				binding.NetworkPairingFile = "packet.dpapi"
			}
			data, _ := json.Marshal(binding)
			if kind == "unknown" {
				data = append(data[:len(data)-1], []byte(`,"secret":"must not be accepted"}`)...)
			}
			if kind == "trailing" {
				data = append(data, []byte(` {}`)...)
			}
			if err := os.WriteFile(WindowsProfilePath(root), data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadWindowsProfile(root); err == nil {
				t.Fatal("unsafe binding accepted")
			}
		})
	}
}

func TestWindowsBindingKeepsPacketPairingOutOfArguments(t *testing.T) {
	root := t.TempDir()
	binding := windowsBindingFixture(root)
	binding.NetworkPairingFile = filepath.Join(root, "packet.dpapi")
	if err := BindWindowsProfile(root, binding); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadWindowsProfile(root)
	if err != nil || loaded.NetworkPairingFile != binding.NetworkPairingFile {
		t.Fatal("packet pairing binding lost")
	}
	cmd, err := WindowsRelayCommand(context.Background(), root, "network-stream")
	if err != nil || len(cmd.Args) != 4 || cmd.Args[3] != "network-stream" {
		t.Fatal("packet transport bypassed the protected profile")
	}
}
