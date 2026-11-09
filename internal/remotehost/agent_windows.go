//go:build windows

package remotehost

import (
	"context"
	"github.com/Microsoft/go-winio"
	"net"
	"os"
	"strings"
)

func dialAgent(ctx context.Context) (net.Conn, error) {
	path := os.Getenv("SSH_AUTH_SOCK")
	if path == "" {
		path = `\\.\pipe\openssh-ssh-agent`
	}
	if strings.HasPrefix(path, `\\.\pipe\`) {
		return winio.DialPipeContext(ctx, path)
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", path)
}
