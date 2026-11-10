//go:build windows

package guestos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

func windowsVMResources(ctx context.Context, root string, memoryGiB, cpus int) (WindowsVMResourceState, error) {
	if (memoryGiB == 0) != (cpus == 0) || memoryGiB < 0 || memoryGiB > 1024 || cpus < 0 || cpus > 1024 {
		return WindowsVMResourceState{}, errors.New("Windows 客机资源参数无效")
	}
	binding, err := LoadWindowsProfile(root)
	if err != nil {
		return WindowsVMResourceState{}, err
	}
	if binding == nil {
		return WindowsVMResourceState{}, errors.New("此配置尚未绑定Windows客机")
	}
	var result WindowsVMResourceState
	if err := windowsHostManagerCall(ctx, binding.VMID, "resources", memoryGiB, cpus, &result); err != nil {
		return result, err
	}
	if result.CPUs < 1 || result.MemoryStartup < 2<<30 || result.State == "" {
		return result, errors.New("Windows 客机资源状态无效")
	}
	if memoryGiB != 0 && !result.MatchesAllocation(memoryGiB, cpus) {
		return result, errors.New("Windows 客机资源未达到请求值")
	}
	return result, nil
}

func windowsVMResourcesNative(ctx context.Context, id uuid.UUID, memoryGiB, cpus int) (WindowsVMResourceState, error) {
	if id == uuid.Nil || (memoryGiB == 0) != (cpus == 0) || memoryGiB < 0 || memoryGiB > 1024 || cpus < 0 || cpus > 1024 {
		return WindowsVMResourceState{}, errors.New("Windows 客机资源参数无效")
	}
	script := fmt.Sprintf("$targetId=[guid]'%s'\n$memoryGiB=%d\n$cpus=%d\n", id.String(), memoryGiB, cpus) + windowsVMResourcesScript
	cmd, err := systemPowerShellQuery(ctx, script)
	if err != nil {
		return WindowsVMResourceState{}, err
	}
	data, err := cmd.Output()
	if ctx.Err() != nil {
		return WindowsVMResourceState{}, ctx.Err()
	}
	if err != nil {
		return WindowsVMResourceState{}, errors.New("Windows 客机资源操作未完成，请核对虚拟机状态与管理权限")
	}
	var result WindowsVMResourceState
	if json.Unmarshal(data, &result) != nil || result.CPUs < 1 || result.MemoryStartup < 2<<30 {
		return result, errors.New("Windows 客机资源状态无效")
	}
	if memoryGiB != 0 && !result.MatchesAllocation(memoryGiB, cpus) {
		return result, errors.New("Windows 客机资源未达到请求值")
	}
	return result, nil
}
