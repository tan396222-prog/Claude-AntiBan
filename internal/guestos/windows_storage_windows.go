//go:build windows

package guestos

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"golang.org/x/sys/windows"
)

func windowsHostStorage(ctx context.Context, id uuid.UUID, input windowsHostStorageRequest) (windowsHostStorageState, error) {
	var state windowsHostStorageState
	err := windowsHostManagerRequestCall(ctx, windowsHostManagerRequest{Version: windowsHostManagerVersion, VMID: id, Operation: "storage", Storage: &input}, &state)
	return state, err
}

func WindowsPrivateStoragePaths() []string {
	return []string{filepath.Join(managerKnownFolder(windows.FOLDERID_ProgramData), "Cage", "HyperVManager")}
}

func replaceWindowsStorageProfile(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".windows-storage-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err = protectWindowsProfile(file.Name()); err != nil {
		file.Close()
		return err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	if err = errors.Join(err, file.Close()); err != nil {
		return err
	}
	from, err := windows.UTF16PtrFromString(file.Name())
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
