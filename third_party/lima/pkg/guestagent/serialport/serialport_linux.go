// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package serialport

import (
	"os"

	"golang.org/x/sys/unix"
)

func openPort(name string) (p *Port, err error) {
	f, err := os.OpenFile(name, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0o666)
	if err != nil {
		return nil, err
	}

	// Keep the descriptor nonblocking for Go's poller. Closing the listener
	// must interrupt a read even when the host has stopped sending data.
	return &Port{f: f}, nil
}

type Port struct {
	f *os.File
}

func (p *Port) Read(b []byte) (n int, err error) {
	return p.f.Read(b)
}

func (p *Port) Write(b []byte) (n int, err error) {
	return p.f.Write(b)
}

func (p *Port) Close() (err error) {
	return p.f.Close()
}
