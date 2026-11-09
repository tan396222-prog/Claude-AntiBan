//go:build darwin && !no_vz

// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package vz

import (
	"context"
	"errors"
	"runtime"

	"github.com/Code-Hex/vz/v3"
	"github.com/docker/go-units"
)

func (l *LimaVzDriver) SetMemoryTarget(ctx context.Context, target uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	maximum, err := units.RAMInBytes(*l.Instance.Config.Memory)
	if err != nil {
		return err
	}
	if target < 512<<20 || target > uint64(maximum) || target%(1<<20) != 0 {
		return errors.New("memory target is outside the VM capacity")
	}
	if l.machine == nil || l.machine.State() != vz.VirtualMachineStateRunning {
		return errors.New("VM is not running")
	}
	for _, device := range l.machine.MemoryBalloonDevices() {
		if balloon := vz.AsVirtioTraditionalMemoryBalloonDevice(device); balloon != nil {
			// go-vz v3.7.1 wraps a borrowed NSArray member without retaining it,
			// but installs a releasing finalizer. The VM owns this native device.
			// Do not let temporary wrappers release the VM's reference.
			runtime.SetFinalizer(balloon, nil)
			balloon.SetTargetVirtualMachineMemorySize(target)
			runtime.KeepAlive(l.machine)
			return nil
		}
	}
	return errors.New("memory balloon is unavailable")
}
