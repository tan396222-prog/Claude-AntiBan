package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sylvinus/agent-vm/internal/vm"
	"github.com/sylvinus/agent-vm/internal/vm/vmtest"
)

func TestLinuxExistingVMChecksPrerequisitesBeforeBoot(t *testing.T) {
	saved := skipPrereqs
	t.Cleanup(func() { skipPrereqs = saved })
	for _, missing := range []string{"QEMU", "KVM"} {
		t.Run(missing, func(t *testing.T) {
			if missing == "KVM" {
				if _, err := os.Stat("/dev/kvm"); !os.IsNotExist(err) {
					t.Skip("requires a Linux host without /dev/kvm")
				}
			}
			bin := t.TempDir()
			if missing == "KVM" {
				qemu := "qemu-system-x86_64"
				if runtime.GOARCH == "arm64" {
					qemu = "qemu-system-aarch64"
				}
				for _, name := range []string{qemu, "qemu-img"} {
					if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
						t.Fatal(err)
					}
				}
			}
			t.Setenv("PATH", bin)
			fake := vmtest.New(&vm.Instance{Name: "existing", Status: vm.Stopped})
			te := newTestEnv(t, fake)
			s := &start{app: te.app, ctx: context.Background(), name: "existing"}
			skipPrereqs = false
			if err := s.boot(); !errors.Is(err, errStop) {
				t.Fatalf("missing %s did not stop existing VM boot: %v", missing, err)
			}
			if strings.Contains(fake.CallLog(), "start ") || fake.VMs["existing"].Status != vm.Stopped {
				t.Fatalf("driver started before prerequisite failure: %s", fake.CallLog())
			}
			if missing == "KVM" && !strings.Contains(te.stderr.String(), "/dev/kvm") {
				t.Fatalf("missing KVM diagnostic: %s", te.stderr.String())
			}
			// The test-only bypass still permits intentional TCG/fake-driver tests.
			skipPrereqs = true
			if err := s.boot(); err != nil || fake.VMs["existing"].Status != vm.Running {
				t.Fatalf("controlled prerequisite bypass failed: %v", err)
			}
		})
	}
}
