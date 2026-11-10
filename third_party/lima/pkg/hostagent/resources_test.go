// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package hostagent

import (
	"context"
	"runtime"
	"slices"
	"testing"

	"github.com/lima-vm/sshocker/pkg/ssh"
)

func TestResourceProbeCommand(t *testing.T) {
	agent := HostAgent{
		sshLocalPort: 60022, instSSHAddress: "127.0.0.1",
		sshConfig: &ssh.SSHConfig{AdditionalArgs: []string{
			"-o", "IdentityFile=private-instance-key", "-o", "ControlMaster=auto",
			"-o", "ControlPath=instance/ssh.sock", "-o", "ControlPersist=5m",
		}},
	}
	configured := slices.Clone(agent.sshConfig.Args())
	const probe = "cat /proc/meminfo"
	cmd := agent.resourceProbeCommand(context.Background(), probe)
	want := slices.Clone(configured)
	if runtime.GOOS == "windows" {
		want = []string{
			"-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ControlPersist=no",
			"-o", "IdentityFile=private-instance-key",
		}
	}
	want = append(want, "-o", "ConnectTimeout=2", "-o", "ClearAllForwardings=yes", "-T", "-p", "60022", "127.0.0.1", "--", probe)
	if !slices.Equal(cmd.Args[1:], want) {
		t.Fatalf("probe arguments: got %q, want %q", cmd.Args[1:], want)
	}
	if !slices.Equal(configured, agent.sshConfig.Args()) {
		t.Fatal("resource probe changed the SSH configuration used by other host-agent tasks")
	}
}
