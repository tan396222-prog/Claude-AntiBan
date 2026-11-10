package remotehost

import "golang.org/x/sys/windows"

// A refused TCP dial returns the Winsock error, not syscall.ECONNREFUSED.
const connectionRefused = windows.WSAECONNREFUSED
