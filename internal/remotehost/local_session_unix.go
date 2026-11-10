//go:build !windows

package remotehost

import "syscall"

const connectionRefused = syscall.ECONNREFUSED
