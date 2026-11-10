// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package hostagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestResourceProbeProcess(t *testing.T) {
	if mode := os.Getenv("CAGE_RESOURCE_PROBE_TEST"); mode != "" {
		switch mode {
		case "failure":
			fmt.Fprint(os.Stdout, "private-stdout")
			fmt.Fprint(os.Stderr, "getsockname failed: Not a socket\n"+strings.Repeat("x", 1<<20))
			os.Exit(7)
		case "timeout":
			time.Sleep(30 * time.Second)
		case "oversize":
			fmt.Fprint(os.Stdout, strings.Repeat("x", 16385))
		case "success":
			fmt.Fprint(os.Stdout, "metrics")
		}
		os.Exit(0)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"failure", "timeout", "oversize", "success", "start"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if mode == "timeout" {
				ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
			}
			cmd := exec.CommandContext(ctx, exe, "-test.run=^TestResourceProbeProcess$")
			cmd.Env = append(os.Environ(), "CAGE_RESOURCE_PROBE_TEST="+mode)
			cmd.WaitDelay = time.Second
			hideResourceProbe(cmd)
			if mode == "start" {
				cmd.Path = "missing-resource-probe-executable"
			}
			out, err := runResourceProbe(ctx, cmd)
			switch mode {
			case "success":
				if err != nil || string(out) != "metrics" {
					t.Fatalf("valid observation lost: %q %v", out, err)
				}
			case "failure":
				if err == nil || !strings.Contains(err.Error(), "Not a socket") || !strings.Contains(err.Error(), "exit status 7") || len(err.Error()) > 4300 || strings.Contains(err.Error(), "private-stdout") {
					t.Fatalf("missing or unbounded execution diagnostic: %v", err)
				}
			case "timeout":
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("timeout cause lost: %v", err)
				}
			case "oversize":
				if err == nil || !strings.Contains(err.Error(), "exceeded") {
					t.Fatalf("oversized stdout accepted: %v", err)
				}
			case "start":
				if err == nil || !strings.Contains(err.Error(), "probe start") {
					t.Fatalf("start failure lost: %v", err)
				}
			}
		})
	}
}
