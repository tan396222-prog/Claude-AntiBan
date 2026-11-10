//go:build windows

package guestos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// WindowsVMAction controls only the already paired VM. It never creates,
// discovers, force-powers-off, removes, or changes a guest's network adapters.
func WindowsVMAction(ctx context.Context, root, action string) error {
	if action != "start" && action != "stop" {
		return errors.New("Windows 客机操作无效")
	}
	binding, err := LoadWindowsProfile(root)
	if err != nil {
		return err
	}
	if binding == nil {
		return errors.New("此配置尚未绑定 Windows 客机")
	}
	operation := action
	if operation == "stop" {
		operation = "shutdown"
	}
	var result WindowsVMResourceState
	if err := windowsHostManagerCall(ctx, binding.VMID, operation, 0, 0, &result); err != nil {
		return err
	}
	if action == "start" && result.State != "Running" || action == "stop" && result.State != "Off" {
		return errors.New("Windows 客机未到达预期状态")
	}
	return nil
}

func windowsVMActionNative(ctx context.Context, id uuid.UUID, action string) error {
	if id == uuid.Nil || action != "start" && action != "stop" {
		return errors.New("Windows 客机操作无效")
	}
	script := fmt.Sprintf("$targetId = [guid]'%s'\n$action = '%s'\n", id.String(), action) + windowsVMLifecycleScript
	cmd, err := systemPowerShellQuery(ctx, script)
	if err != nil {
		return err
	}
	data, err := cmd.Output()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return errors.New("Windows 客机操作未完成，请检查绑定虚拟机的状态与管理权限")
	}
	var result struct {
		State string `json:"state"`
	}
	if json.Unmarshal(data, &result) != nil || (action == "start" && result.State != "Running") || (action == "stop" && result.State != "Off") {
		return errors.New("Windows 客机未到达预期状态")
	}
	return nil
}
