//go:build !windows

package guestos

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

func InspectHyperV(context.Context, uuid.UUID) (HyperVInspection, error) {
	return HyperVInspection{}, errors.New("--vm-id 只支持查询本机 Windows Hyper-V 虚拟机")
}
