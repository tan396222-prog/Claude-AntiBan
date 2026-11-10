//go:build !windows

package guestos

import (
	"context"
	"errors"
	"os/exec"
)

func WindowsInstallCommand(context.Context, string, string, Installation) (*exec.Cmd, error) {
	return nil, errors.New("Windows 客机需要 Windows 11 x64 Hyper-V 主机")
}
