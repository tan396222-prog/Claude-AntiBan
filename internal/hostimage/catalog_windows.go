package hostimage

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

func expandWindowsCatalog(ctx context.Context, data []byte) ([]byte, error) {
	directory, err := os.MkdirTemp("", "cage-system-catalog-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	archive := filepath.Join(directory, "products.cab")
	if err = os.WriteFile(archive, data, 0600); err != nil {
		return nil, err
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, filepath.Join(system, "expand.exe"), "-F:products.xml", archive, directory)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	if err = command.Run(); err != nil {
		return nil, err
	}
	path := filepath.Join(directory, "products.xml")
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 8<<20 {
		return nil, errors.New("微软安装目录文件无效")
	}
	return os.ReadFile(path)
}
