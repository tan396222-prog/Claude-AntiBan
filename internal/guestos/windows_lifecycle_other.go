//go:build !windows

package guestos

import (
	"context"
	"errors"
)

func WindowsVMAction(context.Context, string, string) error {
	return errors.New("Windows 客机生命周期需要 Windows Hyper-V 主机")
}
