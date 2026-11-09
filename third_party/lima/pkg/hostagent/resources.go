// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package hostagent

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/lima-vm/lima/v2/pkg/driver"
	"github.com/lima-vm/lima/v2/pkg/limatype"
)

// ManageResources is an optional embedding hook. It starts after the driver and
// stops before it. Guest observations use the existing private SSH transport.
var ManageResources func(context.Context, string, *limatype.LimaYAML, driver.MemoryBalloon, func(context.Context, string) ([]byte, error)) func()

func (a *HostAgent) startResourceManager(ctx context.Context) func() {
	if ManageResources == nil {
		return func() {}
	}
	d := a.driver
	if configured, ok := d.(*driver.ConfiguredDriver); ok {
		d = configured.Driver
	}
	balloon, ok := d.(driver.MemoryBalloon)
	if !ok {
		return func() {}
	}
	read := func(ctx context.Context, script string) ([]byte, error) {
		args := append([]string{}, a.sshConfig.Args()...)
		args = append(args, "-o", "ConnectTimeout=2", "-o", "ClearAllForwardings=yes", "-T", "-p", strconv.Itoa(a.sshLocalPort), a.instSSHAddress, "--", script)
		cmd := exec.CommandContext(ctx, a.sshConfig.Binary(), args...)
		hideResourceProbe(cmd)
		cmd.WaitDelay = time.Second
		pipe, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		if err = cmd.Start(); err != nil {
			return nil, err
		}
		out, readErr := io.ReadAll(io.LimitReader(pipe, 16385))
		if readErr != nil || len(out) > 16384 {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return nil, errors.New("resource observation exceeded its limit")
		}
		return out, cmd.Wait()
	}
	stop := ManageResources(ctx, a.instDir, a.instConfig, balloon, read)
	var once sync.Once
	return func() { once.Do(stop) }
}
