//go:build !linux

package containerenv

import (
	"context"
	"errors"
)

func (r *Runtime) ensureResourceSlice(context.Context) error {
	return errors.New("容器资源边界仅支持 Linux")
}
func (r *Runtime) verifyHostResources(context.Context, string) (resourceEvidence, error) {
	return resourceEvidence{}, errors.New("容器资源边界仅支持 Linux")
}
