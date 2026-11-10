//go:build !windows

package hostimage

import (
	"context"
	"errors"
)

func expandWindowsCatalog(context.Context, []byte) ([]byte, error) {
	return nil, errors.New("Windows 安装目录需要 Windows 的系统解压组件")
}
