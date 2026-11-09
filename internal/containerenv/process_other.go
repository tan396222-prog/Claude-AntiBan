//go:build !linux

package containerenv

import (
	"context"
	"errors"
	"os/exec"
)

func detachWorker(*exec.Cmd) {}
func tryLockRuntime(string) (func(), error) {
	return nil, errors.New("容器运行环境仅支持 Linux 服务器")
}
func lockRuntime(context.Context, string) (func(), error) {
	return nil, errors.New("容器运行环境仅支持 Linux 服务器")
}
