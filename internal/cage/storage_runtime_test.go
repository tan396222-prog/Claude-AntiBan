package cage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type storageRuntimeFixture struct {
	events   []string
	begin    func(StorageTransition) error
	switchVM func(StorageTransition) error
	finish   func(StorageTransition, bool) error
}

func (*storageRuntimeFixture) Name() string { return "test-native-vm" }
func (*storageRuntimeFixture) Snapshot(context.Context, string) (StorageRuntimeSnapshot, error) {
	return StorageRuntimeSnapshot{State: []byte(`{"vm":"original"}`)}, nil
}
func (runtime *storageRuntimeFixture) Begin(_ context.Context, move StorageTransition) error {
	runtime.events = append(runtime.events, "begin")
	if runtime.begin != nil {
		return runtime.begin(move)
	}
	return nil
}
func (runtime *storageRuntimeFixture) Switch(_ context.Context, move StorageTransition) error {
	runtime.events = append(runtime.events, "switch")
	if runtime.switchVM != nil {
		return runtime.switchVM(move)
	}
	return nil
}
func (runtime *storageRuntimeFixture) Recover(_ context.Context, move StorageTransition, committed bool) error {
	action := "rollback"
	if committed {
		action = "commit"
	}
	runtime.events = append(runtime.events, action)
	if runtime.finish != nil {
		return runtime.finish(move, committed)
	}
	return nil
}

func TestStorageRuntimeUsesVerifiedCopyAndRetainsOriginal(t *testing.T) {
	profile, root := newStorageFixture(t)
	payload := []byte("original VM disk and account state")
	file := filepath.Join("native-vm", "system.vhdx")
	writeStorageFixture(t, filepath.Join(profile, "state", file), payload)
	runtime := &storageRuntimeFixture{}
	runtime.switchVM = func(move StorageTransition) error {
		assertStorageFile(t, filepath.Join(move.Source, file), payload)
		assertStorageFile(t, filepath.Join(move.Destination, file), payload)
		if _, err := os.Readlink(storageAnchor(profile)); err == nil {
			t.Fatal("stable path changed before the runtime accepted its new data")
		}
		return nil
	}
	runtime.finish = func(move StorageTransition, committed bool) error {
		if !committed {
			t.Fatal("successful move rolled back")
		}
		assertStorageFile(t, filepath.Join(move.Backup, file), payload)
		actual, err := os.Stat(storageAnchor(profile))
		destination, targetErr := os.Stat(move.Destination)
		if err != nil || targetErr != nil || !os.SameFile(actual, destination) {
			t.Fatalf("runtime committed before directory: anchor %v, destination %v", err, targetErr)
		}
		return nil
	}
	location, err := MoveStorageWithRuntime(context.Background(), profile, filepath.Join(root, "moved"), storageExpectation(t, profile), runtime, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(runtime.events, []string{"begin", "switch", "commit"}) {
		t.Fatal(runtime.events)
	}
	assertStorageFile(t, filepath.Join(location.Backup, file), payload)
}

func TestStorageRuntimeFailureRetainsBothCopiesUntilRecovery(t *testing.T) {
	profile, root := newStorageFixture(t)
	payload := []byte("original")
	file := filepath.Join("native-vm", "system.vhdx")
	writeStorageFixture(t, filepath.Join(profile, "state", file), payload)
	runtime := &storageRuntimeFixture{switchVM: func(StorageTransition) error { return errors.New("runtime attach failed") }}
	var transition StorageTransition
	runtime.finish = func(move StorageTransition, committed bool) error {
		transition = move
		if committed {
			t.Fatal("failed runtime attached as committed")
		}
		assertStorageFile(t, filepath.Join(storageAnchor(profile), file), payload)
		assertStorageFile(t, filepath.Join(move.Destination, file), payload)
		return errors.New("helper disconnected")
	}
	if _, err := MoveStorageWithRuntime(context.Background(), profile, filepath.Join(root, "moved"), storageExpectation(t, profile), runtime, nil); err == nil {
		t.Fatal("failed migration succeeded")
	}
	if _, err := GetStorageLocation(profile); !errors.Is(err, ErrStorageRuntimeRecovery) {
		t.Fatalf("plain caller resolved native transaction: %v", err)
	}
	if _, _, err := AcquireStorageUse(profile); !errors.Is(err, ErrStorageRuntimeRecovery) {
		t.Fatalf("CLI started unresolved VM: %v", err)
	}
	assertStorageFile(t, filepath.Join(transition.Destination, file), payload)
	runtime.finish = func(move StorageTransition, committed bool) error {
		assertStorageFile(t, filepath.Join(storageAnchor(profile), file), payload)
		assertStorageFile(t, filepath.Join(move.Destination, file), payload)
		return nil
	}
	if err := RecoverStorage(context.Background(), profile, runtime); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(transition.Destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("recovered failed copy retained", err)
	}
	assertStorageFile(t, filepath.Join(storageAnchor(profile), file), payload)
}

func TestStorageRuntimeCrashRecoveryUsesDurableCommit(t *testing.T) {
	for _, committed := range []bool{false, true} {
		name := "before_commit"
		if committed {
			name = "after_commit"
		}
		t.Run(name, func(t *testing.T) {
			profile, root := newStorageFixture(t)
			id := "0123456789abcdef0123456789abcdef"
			target := filepath.Join(root, "new")
			payload := []byte("disk data")
			writeStorageFixture(t, filepath.Join(storageAnchor(profile), "system.vhdx"), payload)
			writeStorageFixture(t, filepath.Join(target, "state", "system.vhdx"), payload)
			if err := claimStorageTarget(target, id); err != nil {
				t.Fatal(err)
			}
			if err := claimStorageTarget(filepath.Join(target, "state"), id); err != nil {
				t.Fatal(err)
			}
			backup := filepath.Join(profile, "state-backup-"+id)
			move := storageMove{Next: storageRecord{Directory: target, ID: id, Backups: []string{backup}}, Runtime: "test-native-vm", Transition: StorageTransition{ID: id, Source: storageAnchor(profile), Destination: filepath.Join(target, "state"), Backup: backup, State: []byte(`{"vm":"original"}`)}}
			if err := writeStorageJSON(filepath.Join(profile, pendingFile), move); err != nil {
				t.Fatal(err)
			}
			if err := renameStorage(storageAnchor(profile), backup); err != nil {
				t.Fatal(err)
			}
			if err := linkStorage(storageAnchor(profile), move.Transition.Destination); err != nil {
				t.Fatal(err)
			}
			if committed {
				if err := writeStorageJSON(filepath.Join(profile, locationFile), move.Next); err != nil {
					t.Fatal(err)
				}
			}
			runtime := &storageRuntimeFixture{finish: func(transition StorageTransition, actual bool) error {
				if actual != committed {
					t.Fatal("runtime used a different commit decision")
				}
				assertStorageFile(t, filepath.Join(storageAnchor(profile), "system.vhdx"), payload)
				assertStorageFile(t, filepath.Join(transition.Destination, "system.vhdx"), payload)
				_, linkErr := os.Readlink(storageAnchor(profile))
				if committed != (linkErr == nil) {
					t.Fatal("directory rollback happened after runtime recovery")
				}
				return nil
			}}
			if err := RecoverStorage(context.Background(), profile, runtime); err != nil {
				t.Fatal(err)
			}
			if err := RecoverStorage(context.Background(), profile, runtime); err != nil {
				t.Fatal("idempotent recovery failed", err)
			}
			if len(runtime.events) != 1 {
				t.Fatal("completed recovery repeated a VM operation", runtime.events)
			}
		})
	}
}
