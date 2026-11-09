//go:build !windows

package vm

import (
	"context"

	"github.com/lima-vm/lima/v2/pkg/instance"
	"github.com/lima-vm/lima/v2/pkg/limatype"
)

func stopInstance(ctx context.Context, inst *limatype.Instance) error {
	return instance.StopGracefully(ctx, inst, false)
}

func stopOrphanedInstance(_ context.Context, inst *limatype.Instance) error {
	instance.StopForcibly(inst)
	return nil
}
