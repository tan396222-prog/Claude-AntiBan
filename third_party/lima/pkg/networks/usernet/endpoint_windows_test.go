//go:build windows

// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package usernet

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestNetstackWindowsNativeSocketPaths(t *testing.T) {
	// A drive letter, spaces and URL punctuation must remain a file path.
	dir, err := os.MkdirTemp("", "cage %23-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	endpoint := filepath.Join(dir, "ep.sock")
	qemu := filepath.Join(dir, "qemu.sock")
	if err := StartGVisorNetstack(ctx, &GVisorNetstackOpts{
		MTU: 1500, Endpoint: endpoint, QemuSocket: qemu,
		Subnet: "192.168.127.0/24", Async: true,
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{endpoint, qemu} {
		conn, err := net.Dial("unix", path)
		if err != nil {
			t.Fatalf("cannot connect to native socket %q: %v", path, err)
		}
		conn.Close()
	}
}
