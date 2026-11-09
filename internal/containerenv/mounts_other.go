//go:build !linux

package containerenv

import (
	"context"
	"errors"
)

func (r *Runtime) inspectMounts(*engineContainer) (MountPropagation, error) {
	return MountPropagation{}, errors.New("容器挂载检查仅支持 Linux")
}
func (r *Runtime) initializeMounts(context.Context, string) error {
	return errors.New("容器挂载初始化仅支持 Linux")
}
