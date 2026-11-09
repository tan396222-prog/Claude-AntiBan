package vm

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/lima-vm/lima/v2/pkg/limatype"
	"github.com/lima-vm/lima/v2/pkg/store"
	"github.com/sylvinus/agent-vm/internal/host"
	"golang.org/x/sys/windows"
)

func stopInstance(ctx context.Context, inst *limatype.Instance) error {
	requested, err := host.RequestInstanceStop(inst.Dir)
	if err != nil {
		return err
	}
	if !requested {
		return fmt.Errorf("Cage 环境的停止通知不可用；保留环境，请查看 hostagent 日志")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute+10*time.Second)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待 Cage 环境保存并停止超时: %w", ctx.Err())
		case <-ticker.C:
			current, err := store.Inspect(ctx, inst.Name)
			if err != nil {
				return err
			}
			if current.Status == limatype.StatusStopped {
				return nil
			}
		}
	}
}

func stopOrphanedInstance(_ context.Context, inst *limatype.Instance) error {
	if inst.VMType != limatype.QEMU || inst.DriverPID <= 0 {
		return fmt.Errorf("unsupported orphaned VM driver")
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(inst.DriverPID))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	image := make([]uint16, 32768)
	size := uint32(len(image))
	if err := windows.QueryFullProcessImageName(handle, 0, &image[0], &size); err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Base(windows.UTF16ToString(image[:size])), "qemu-system-x86_64.exe") || !isQEMU(inst.DriverPID, inst.Dir, inst.Name) {
		return fmt.Errorf("orphaned VM process identity changed; no process was stopped")
	}
	if err := windows.TerminateProcess(handle, 1); err != nil {
		return err
	}
	state, err := windows.WaitForSingleObject(handle, 5000)
	if err != nil {
		return err
	}
	if state != windows.WAIT_OBJECT_0 {
		return fmt.Errorf("orphaned QEMU did not stop")
	}
	return nil
}
