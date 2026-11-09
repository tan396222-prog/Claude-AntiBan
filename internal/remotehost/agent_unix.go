//go:build !windows

package remotehost

import (
	"context"
	"errors"
	"net"
	"os"
)

func dialAgent(ctx context.Context) (net.Conn, error) {
	path := os.Getenv("SSH_AUTH_SOCK")
	if path == "" {
		return nil, errors.New("SSH agent unavailable")
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", path)
}
