//go:build darwin && (!arm64 || !cgo || no_vz)

package guestos

import (
	"context"
	"errors"
)

func probeMacHost(ctx context.Context) (MacRestore, error) {
	if err := ctx.Err(); err != nil {
		return MacRestore{}, err
	}
	return MacRestore{}, errors.New("macOS guests require Apple silicon and a Cage build with native Virtualization support")
}
