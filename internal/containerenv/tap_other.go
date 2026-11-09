//go:build !linux

package containerenv

import (
	"context"
	"errors"
	"io"
)

func RunTAP(context.Context, io.Reader, io.Writer) error {
	return errors.New("Cage 容器运行环境仅支持 Linux 服务器")
}
