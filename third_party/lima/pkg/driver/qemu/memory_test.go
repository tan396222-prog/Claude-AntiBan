// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package qemu

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lima-vm/lima/v2/pkg/limatype"
	"github.com/lima-vm/lima/v2/pkg/limatype/filenames"
)

func TestMemoryTargetQMPProtocolAndCancellation(t *testing.T) {
	for _, stall := range []bool{false, true} {
		t.Run(map[bool]string{false: "command", true: "cancelled"}[stall], func(t *testing.T) {
			dir, err := os.MkdirTemp("", "cqm-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			memory := "4GiB"
			l := &LimaQemuDriver{Instance: &limatype.Instance{Dir: dir, Config: &limatype.LimaYAML{Memory: &memory}}}
			listener, err := net.Listen("unix", filepath.Join(dir, filenames.QMPSock))
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() {
				conn, e := listener.Accept()
				if e != nil {
					done <- e
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(time.Second))
				if stall {
					var b [1]byte
					_, e = conn.Read(b[:])
					done <- e
					return
				}
				enc, dec := json.NewEncoder(conn), json.NewDecoder(conn)
				_ = enc.Encode(map[string]any{"QMP": map[string]any{}})
				for i := 1; i <= 2; i++ {
					var request struct {
						ID        int    `json:"id"`
						Execute   string `json:"execute"`
						Arguments struct {
							Value uint64 `json:"value"`
						} `json:"arguments"`
					}
					if e = dec.Decode(&request); e != nil {
						done <- e
						return
					}
					if request.ID != i || (i == 1 && request.Execute != "qmp_capabilities") || (i == 2 && (request.Execute != "balloon" || request.Arguments.Value != 2<<30)) {
						t.Error("wrong QMP request", request)
					}
					_ = enc.Encode(map[string]any{"event": "BALLOON_CHANGE"})
					_ = enc.Encode(map[string]any{"id": i, "return": map[string]any{}})
				}
				done <- nil
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			started := time.Now()
			err = l.SetMemoryTarget(ctx, 2<<30)
			if (err != nil) != stall {
				t.Fatal("unexpected command result", err)
			}
			if time.Since(started) > time.Second {
				t.Fatal("deadline ignored")
			}
			if e := <-done; e != nil && !stall {
				t.Fatal(e)
			}
			if err := l.SetMemoryTarget(context.Background(), 5<<30); err == nil {
				t.Fatal("capacity exceeded")
			}
		})
	}
}
