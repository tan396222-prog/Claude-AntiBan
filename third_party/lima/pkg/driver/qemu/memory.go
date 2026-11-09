// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package qemu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"time"

	"github.com/docker/go-units"
	"github.com/lima-vm/lima/v2/pkg/limatype/filenames"
)

func (l *LimaQemuDriver) SetMemoryTarget(ctx context.Context, target uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	maximum, err := units.RAMInBytes(*l.Instance.Config.Memory)
	if err != nil {
		return err
	}
	if target < 512<<20 || target > uint64(maximum) || target%(1<<20) != 0 {
		return errors.New("memory target is outside the VM capacity")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(l.Instance.Dir, filenames.QMPSock))
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	decoder := json.NewDecoder(io.LimitReader(conn, 64<<10))
	var greeting map[string]json.RawMessage
	if err := decoder.Decode(&greeting); err != nil {
		return err
	}
	if greeting["QMP"] == nil {
		return errors.New("invalid QMP greeting")
	}
	encoder := json.NewEncoder(conn)
	for i, command := range []string{"qmp_capabilities", "balloon"} {
		request := map[string]any{"execute": command, "id": i + 1}
		if command == "balloon" {
			request["arguments"] = map[string]uint64{"value": target}
		}
		if err := encoder.Encode(request); err != nil {
			return err
		}
		for {
			var response struct {
				ID    int `json:"id"`
				Error *struct {
					Class string `json:"class"`
				} `json:"error"`
			}
			if err := decoder.Decode(&response); err != nil {
				return err
			}
			if response.ID != i+1 {
				continue
			}
			if response.Error != nil {
				return fmt.Errorf("QMP memory adjustment failed: %s", response.Error.Class)
			}
			break
		}
	}
	return nil
}
