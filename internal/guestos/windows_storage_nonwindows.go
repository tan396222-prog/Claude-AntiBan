//go:build !windows

package guestos

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

func windowsHostStorage(context.Context, uuid.UUID, windowsHostStorageRequest) (windowsHostStorageState, error) {
	return windowsHostStorageState{}, errors.New("Windows 客机存储迁移需要 Windows Hyper-V 主机")
}

func WindowsPrivateStoragePaths() []string { return nil }

func prepareWindowsStorageDisk(context.Context, string, string) error { return nil }

func RepairWindowsHostManager(context.Context, string, string, io.Writer, io.Writer) error {
	return errors.New("Windows 系统助手需要在原 Windows 主机上恢复")
}

func replaceWindowsStorageProfile(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".windows-storage-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	if err = errors.Join(err, file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
