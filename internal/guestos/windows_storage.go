package guestos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/paths"
)

type windowsStorageSnapshot struct {
	Profile  WindowsProfile     `json:"profile"`
	Instance string             `json:"instance"`
	Disk     windowsStorageDisk `json:"disk"`
	Excluded []string           `json:"excluded,omitempty"`
}

type windowsStorageRuntime struct {
	profile string
	call    func(context.Context, uuid.UUID, windowsHostStorageRequest) (windowsHostStorageState, error)
}

func WindowsStorageRuntime(profile string) cage.StorageRuntime {
	return &windowsStorageRuntime{profile: profile, call: windowsHostStorage}
}

// RecoverWindowsStorage is safe on ordinary startup. The helper is contacted
// only when a durable Windows storage transaction still needs resolution.
func RecoverWindowsStorage(ctx context.Context, profile string) error {
	return cage.RecoverStorage(ctx, profile, WindowsStorageRuntime(profile))
}

func (*windowsStorageRuntime) Name() string { return "windows-hyperv" }

func (runtime *windowsStorageRuntime) Snapshot(ctx context.Context, source string) (cage.StorageRuntimeSnapshot, error) {
	var snapshot cage.StorageRuntimeSnapshot
	binding, err := LoadWindowsProfile(runtime.profile)
	if err != nil {
		return snapshot, err
	}
	if binding == nil {
		return snapshot, errors.New("Windows 客机尚未绑定")
	}
	state, err := runtime.call(ctx, binding.VMID, windowsHostStorageRequest{Action: "status"})
	if err != nil {
		return snapshot, fmt.Errorf("请先在设置的环境页选择“同步环境组件”，完成 Windows 系统授权后再迁移：%w", err)
	}
	if state.Pending != "" {
		return snapshot, errors.New("这台 Windows 客机仍有待恢复的存储迁移，请打开发起迁移的原工作台")
	}
	instance, err := paths.Resolve(binding.InstanceDirectory)
	if err != nil {
		return snapshot, err
	}
	runtimeState := windowsStorageSnapshot{Profile: *binding, Instance: instance, Disk: state.Disk}
	if !cage.StorageContains(source, instance) {
		snapshot.Backups = append(snapshot.Backups, instance)
	}
	if !cage.StorageContains(source, state.Disk.Path) && !cage.StorageContains(instance, state.Disk.Path) {
		snapshot.Backups = append(snapshot.Backups, state.Disk.Path)
	}
	if state.Layout != nil {
		// Hyper-V moves and backs up its own configuration and snapshot files.
		// Ordinary host-user copies must not inherit or replace their ACLs.
		for _, directory := range []string{filepath.Join(state.Layout.Configuration, "Virtual Machines"), filepath.Join(state.Layout.Snapshots, "Snapshots")} {
			physical, resolveErr := cage.StoragePhysicalPath(directory)
			if resolveErr != nil {
				return snapshot, resolveErr
			}
			if cage.StorageContains(source, physical) && !cage.StorageContains(physical, state.Disk.Path) {
				snapshot.Excluded = append(snapshot.Excluded, physical)
			} else if cage.StorageContains(instance, physical) && !cage.StorageContains(physical, state.Disk.Path) {
				runtimeState.Excluded = append(runtimeState.Excluded, physical)
			}
		}
	}
	snapshot.State, err = json.Marshal(runtimeState)
	return snapshot, err
}

func windowsMoveSnapshot(move cage.StorageTransition) (windowsStorageSnapshot, error) {
	var snapshot windowsStorageSnapshot
	if json.Unmarshal(move.State, &snapshot) != nil || snapshot.Profile.validate() != nil ||
		!filepath.IsAbs(snapshot.Instance) || !filepath.IsAbs(snapshot.Disk.Path) || snapshot.Disk.Volume == 0 || snapshot.Disk.FileID == 0 {
		return snapshot, errors.New("Windows 存储迁移的恢复信息无效")
	}
	return snapshot, nil
}

func (runtime *windowsStorageRuntime) action(ctx context.Context, move cage.StorageTransition, action, disk string) error {
	snapshot, err := windowsMoveSnapshot(move)
	if err != nil {
		return err
	}
	state, err := runtime.call(ctx, snapshot.Profile.VMID, windowsHostStorageRequest{Action: action, ID: move.ID,
		SourceVolume: snapshot.Disk.Volume, SourceFileID: snapshot.Disk.FileID, Disk: disk})
	if err != nil {
		return err
	}
	if action == "begin" || action == "attach" {
		if state.Pending != move.ID {
			return errors.New("Windows 管理助手没有保留当前迁移事务")
		}
	} else if state.Pending != "" || state.Outcome != action {
		return errors.New("Windows 管理助手尚未完成迁移恢复")
	}
	return nil
}

func (runtime *windowsStorageRuntime) Begin(ctx context.Context, move cage.StorageTransition) error {
	return runtime.action(ctx, move, "begin", "")
}

func windowsMovedPath(value, source, target string) string {
	if !cage.StorageContains(source, value) {
		return value
	}
	rel, err := filepath.Rel(source, value)
	if err != nil {
		return value
	}
	return filepath.Join(target, rel)
}

func (runtime *windowsStorageRuntime) movedProfile(move cage.StorageTransition, snapshot windowsStorageSnapshot) (WindowsProfile, string) {
	logical := filepath.Join(runtime.profile, "state")
	instance := filepath.Join(move.Destination, "windows-"+strings.ReplaceAll(snapshot.Profile.VMID.String(), "-", ""))
	if cage.StorageContains(move.Source, snapshot.Instance) {
		instance = windowsMovedPath(snapshot.Instance, move.Source, move.Destination)
	}
	next := snapshot.Profile
	next.InstanceDirectory = instance
	remap := func(value string) string {
		value = windowsMovedPath(value, snapshot.Profile.InstanceDirectory, instance)
		value = windowsMovedPath(value, snapshot.Instance, instance)
		value = windowsMovedPath(value, move.Source, move.Destination)
		return windowsMovedPath(value, logical, move.Destination)
	}
	next.RelayExecutable = remap(next.RelayExecutable)
	next.PairingFile = remap(next.PairingFile)
	next.NetworkPairingFile = remap(next.NetworkPairingFile)
	disk := windowsMovedPath(snapshot.Disk.Path, snapshot.Instance, instance)
	disk = windowsMovedPath(disk, move.Source, move.Destination)
	if disk == snapshot.Disk.Path {
		disk = filepath.Join(instance, filepath.Base(snapshot.Disk.Path))
	}
	return next, disk
}

func (runtime *windowsStorageRuntime) Switch(ctx context.Context, move cage.StorageTransition) error {
	snapshot, err := windowsMoveSnapshot(move)
	if err != nil {
		return err
	}
	next, disk := runtime.movedProfile(move, snapshot)
	physicalInstance := next.InstanceDirectory
	if !cage.StorageContains(move.Source, snapshot.Instance) {
		if err = cage.CopyStorage(ctx, snapshot.Instance, physicalInstance, snapshot.Excluded, nil); err != nil {
			return err
		}
	}
	if !cage.StorageContains(move.Source, snapshot.Disk.Path) && !cage.StorageContains(snapshot.Instance, snapshot.Disk.Path) {
		if err = os.MkdirAll(physicalInstance, 0700); err != nil {
			return err
		}
		if err = cage.CopyStorageFile(ctx, snapshot.Disk.Path, disk); err != nil {
			return err
		}
	}
	if err = prepareWindowsStorageDisk(ctx, snapshot.Disk.Path, disk); err != nil {
		return err
	}
	for _, path := range []string{next.PairingFile, next.NetworkPairingFile} {
		if path != "" && cage.StorageContains(move.Destination, path) {
			if err = protectWindowsProfile(path); err != nil {
				return err
			}
		}
	}
	if err = runtime.action(ctx, move, "attach", disk); err != nil {
		return err
	}
	return runtime.writeProfile(snapshot.Profile, next, next)
}

func (runtime *windowsStorageRuntime) Recover(ctx context.Context, move cage.StorageTransition, committed bool) error {
	snapshot, err := windowsMoveSnapshot(move)
	if err != nil {
		return err
	}
	next, _ := runtime.movedProfile(move, snapshot)
	chosen, action := snapshot.Profile, "rollback"
	if committed {
		chosen, action = next, "commit"
	}
	if err = runtime.action(ctx, move, action, ""); err != nil {
		return err
	}
	return runtime.writeProfile(snapshot.Profile, next, chosen)
}

func (runtime *windowsStorageRuntime) writeProfile(before, after, chosen WindowsProfile) error {
	current, err := LoadWindowsProfile(runtime.profile)
	if err != nil {
		return err
	}
	if current == nil || *current != before && *current != after {
		return errors.New("Windows 客机配对在迁移期间发生变化，请保留迁移记录并核对原配置")
	}
	if *current == chosen {
		return nil
	}
	data, err := json.Marshal(chosen)
	if err != nil {
		return err
	}
	return replaceWindowsStorageProfile(WindowsProfilePath(runtime.profile), append(data, '\n'))
}
