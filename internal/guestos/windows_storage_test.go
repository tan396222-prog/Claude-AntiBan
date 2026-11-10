package guestos

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/cage"
)

type windowsStorageBrokerFixture struct {
	t            *testing.T
	vm           uuid.UUID
	disk         windowsStorageDisk
	layout       *windowsVMStorageLayout
	before       windowsStorageDisk
	pending      string
	events       []string
	failAttach   bool
	failRecovery bool
}

func (broker *windowsStorageBrokerFixture) call(_ context.Context, id uuid.UUID, input windowsHostStorageRequest) (windowsHostStorageState, error) {
	broker.t.Helper()
	if id != broker.vm || input.validate() != nil {
		broker.t.Fatal("invalid fixed-VM request", input)
	}
	broker.events = append(broker.events, input.Action)
	switch input.Action {
	case "status":
		return windowsHostStorageState{Disk: broker.disk, Pending: broker.pending, Layout: broker.layout}, nil
	case "begin":
		broker.before, broker.pending = broker.disk, input.ID
	case "attach":
		if input.ID != broker.pending {
			broker.t.Fatal("attachment lost migration identity")
		}
		old, err := os.ReadFile(broker.before.Path)
		if err != nil {
			broker.t.Fatal(err)
		}
		copied, err := os.ReadFile(input.Disk)
		if err != nil || !bytes.Equal(old, copied) {
			broker.t.Fatal("runtime attached unverified disk bytes", err)
		}
		broker.disk = windowsStorageDisk{Path: input.Disk, Volume: 2, FileID: broker.before.FileID + 1}
		if broker.failAttach {
			return windowsHostStorageState{}, errors.New("response lost after attachment")
		}
	case "commit", "rollback":
		if broker.failRecovery {
			return windowsHostStorageState{}, errors.New("helper unavailable during recovery")
		}
		if input.Action == "rollback" {
			if _, err := os.Stat(broker.before.Path); err != nil {
				broker.t.Fatal("old stable directory was not restored before VM rollback", err)
			}
			if _, err := os.Stat(broker.disk.Path); err != nil {
				broker.t.Fatal("new disk deleted before VM rollback", err)
			}
			broker.disk = broker.before
		}
		broker.pending = ""
	}
	return windowsHostStorageState{Disk: broker.disk, Pending: broker.pending, Outcome: input.Action}, nil
}

func windowsStorageFixture(t *testing.T, external bool) (string, *windowsStorageRuntime, *windowsStorageBrokerFixture, WindowsProfile) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(root, "profile")
	instance := filepath.Join(profile, "state", "windows-existing")
	if external {
		instance = filepath.Join(root, "existing VM outside profile")
	}
	if err := os.MkdirAll(instance, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(profile, 0700); err != nil {
		t.Fatal(err)
	}
	binding := windowsBindingFixture(profile)
	binding.InstanceDirectory = instance
	binding.RelayExecutable = filepath.Join(instance, "relay.exe")
	binding.PairingFile = filepath.Join(instance, "pairing.dpapi")
	binding.NetworkPairingFile = filepath.Join(instance, "network.dpapi")
	for _, path := range []string{binding.RelayExecutable, binding.PairingFile, binding.NetworkPairingFile} {
		if err := os.WriteFile(path, []byte("synthetic pairing bytes"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	disk := filepath.Join(instance, "system.vhdx")
	createWindowsStorageFixtureDisk(t, disk)
	if err := BindWindowsProfile(profile, binding); err != nil {
		t.Fatal(err)
	}
	broker := &windowsStorageBrokerFixture{t: t, vm: binding.VMID, disk: windowsStorageDisk{Path: disk, Volume: 1, FileID: 11}}
	return profile, &windowsStorageRuntime{profile: profile, call: broker.call}, broker, binding
}

func TestWindowsStorageMigrationPreservesPairingAndPriorDisks(t *testing.T) {
	for _, external := range []bool{false, true} {
		name := "managed_instance"
		if external {
			name = "existing_external_instance"
		}
		t.Run(name, func(t *testing.T) {
			profile, runtime, broker, original := windowsStorageFixture(t, external)
			originalDisk, err := os.ReadFile(broker.disk.Path)
			if err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				location, err := cage.GetStorageLocation(profile)
				if err != nil || !location.HasEnvironment {
					t.Fatal("Windows environment not detected", location, err)
				}
				target := filepath.Join(t.TempDir(), "环境存储")
				moved, err := cage.MoveStorageWithRuntime(context.Background(), profile, target, location, runtime, nil)
				if err != nil {
					t.Fatal(err)
				}
				actual, err := LoadWindowsProfile(profile)
				if err != nil || actual == nil || actual.VMID != original.VMID || actual.GUIUserSID != original.GUIUserSID {
					t.Fatal("migration changed VM or GUI identity", err)
				}
				for _, path := range []string{actual.RelayExecutable, actual.PairingFile, actual.NetworkPairingFile} {
					data, err := os.ReadFile(path)
					if err != nil || string(data) != "synthetic pairing bytes" {
						t.Fatal("pairing changed or lost its stable path", path, err)
					}
				}
				if broker.pending != "" || !moved.Custom || moved.Backup == "" {
					t.Fatal("migration did not finish or retain backup", moved, broker.pending)
				}
				if data, err := os.ReadFile(broker.disk.Path); err != nil || !bytes.Equal(data, originalDisk) {
					t.Fatal("new disk unavailable", err)
				}
				if external && attempt == 0 {
					private, err := cage.StoragePrivatePaths(profile)
					if err != nil {
						t.Fatal(err)
					}
					found := false
					for _, path := range private {
						found = found || path == original.InstanceDirectory
					}
					if !found {
						t.Fatal("external retained disk lost private-data protection", private)
					}
				}
			}
			if !reflect.DeepEqual(broker.events, []string{"status", "begin", "attach", "commit", "status", "begin", "attach", "commit"}) {
				t.Fatal(broker.events)
			}
		})
	}
}

func TestWindowsStorageAttachmentFailureRollsBackAndRetries(t *testing.T) {
	profile, runtime, broker, original := windowsStorageFixture(t, false)
	location, err := cage.GetStorageLocation(profile)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "destination")
	broker.failAttach, broker.failRecovery = true, true
	if _, err := cage.MoveStorageWithRuntime(context.Background(), profile, target, location, runtime, nil); err == nil {
		t.Fatal("lost native response treated as success")
	}
	if _, err := os.Stat(broker.disk.Path); err != nil {
		t.Fatal("unresolved native disk was removed", err)
	}
	if _, err := cage.GetStorageLocation(profile); !errors.Is(err, cage.ErrStorageRuntimeRecovery) {
		t.Fatal("shared loader bypassed native recovery", err)
	}
	broker.failRecovery = false
	if err := cage.RecoverStorage(context.Background(), profile, runtime); err != nil {
		t.Fatal(err)
	}
	actual, err := LoadWindowsProfile(profile)
	if err != nil || actual == nil || *actual != original {
		t.Fatal("rollback changed original pairing", err)
	}
	if _, err := os.Stat(filepath.Join(target, "state")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("resolved failed copy not removed", err)
	}
	broker.failAttach = false
	if _, err := cage.MoveStorageWithRuntime(context.Background(), profile, target, location, runtime, nil); err != nil {
		t.Fatal("retry failed", err)
	}
}

func TestWindowsStorageCannotUseFilesystemOnlyMove(t *testing.T) {
	profile, _, _, _ := windowsStorageFixture(t, false)
	location, err := cage.GetStorageLocation(profile)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "destination")
	if _, err := cage.MoveStorage(context.Background(), profile, target, location, nil); err == nil {
		t.Fatal("filesystem migration left registered VM bound to its old inode")
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rejected move touched destination", err)
	}
}

func TestWindowsStorageExternalMetadataIsOwnedByHyperV(t *testing.T) {
	profile, runtime, broker, original := windowsStorageFixture(t, true)
	metadata := filepath.Join(original.InstanceDirectory, "Virtual Machines")
	if err := os.Mkdir(metadata, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metadata, original.VMID.String()+".vmcx"), []byte("Hyper-V managed configuration"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(metadata, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(metadata, 0700) })
	broker.layout = &windowsVMStorageLayout{Configuration: original.InstanceDirectory, Snapshots: original.InstanceDirectory, Paging: original.InstanceDirectory}
	location, err := cage.GetStorageLocation(profile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cage.MoveStorageWithRuntime(context.Background(), profile, filepath.Join(t.TempDir(), "target"), location, runtime, nil); err != nil {
		t.Fatal("ordinary cold copy traversed native metadata", err)
	}
	next, err := LoadWindowsProfile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(next.InstanceDirectory, "Virtual Machines")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unregistered metadata duplicate copied into user storage", err)
	}
}
