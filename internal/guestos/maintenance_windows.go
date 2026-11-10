//go:build windows

package guestos

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func WindowsMaintenanceCommand(ctx context.Context, profile, kit, manifest string, tools bool) (*exec.Cmd, error) {
	binding, err := LoadWindowsProfile(profile)
	if err != nil {
		return nil, err
	}
	if binding == nil {
		return nil, errors.New("此配置尚未绑定 Windows 客机")
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(manifest)
	if err != nil {
		return nil, err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	args := []string{"-NoLogo", "-NoProfile", "-ExecutionPolicy", "RemoteSigned", "-File", filepath.Join(kit, "maintain-windows-workbench.ps1"), "-Profile", profile, "-ManifestPath", manifest, "-ManifestSHA256", hash}
	if tools {
		args = append(args, "-Tools")
	}
	return exec.CommandContext(ctx, filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe"), args...), nil
}

// RepairWindowsHostManager uses the existing UAC maintenance entry point. It
// updates the fixed host service without requiring a running GUI guest.
func RepairWindowsHostManager(ctx context.Context, profile, kit string, stdout, stderr io.Writer) error {
	manifest, err := RequiredNativeComponents(kit, Windows11)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(profile, "native-host-repair-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	err = json.NewEncoder(file).Encode(manifest)
	if err = errors.Join(err, file.Close()); err != nil {
		return err
	}
	command, err := WindowsMaintenanceCommand(ctx, profile, kit, file.Name(), false)
	if err != nil {
		return err
	}
	command.Args = append(command.Args, "-HostOnly")
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return nil
}
