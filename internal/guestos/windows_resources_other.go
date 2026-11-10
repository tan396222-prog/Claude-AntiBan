//go:build !windows

package guestos

import (
	"context"
	"errors"
)

func windowsVMResources(context.Context, string, int, int) (WindowsVMResourceState, error) {
	return WindowsVMResourceState{}, errors.New("Windows 客机资源管理需要Windows Hyper-V主机")
}
