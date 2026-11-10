//go:build !windows

package guestos

import (
	"context"
	"errors"
	"os/exec"
)

func WindowsMaintenanceCommand(context.Context, string, string, string, bool) (*exec.Cmd, error) {
	return nil, errors.New("Windows 客机维护需要其 Windows Hyper-V 主机")
}
