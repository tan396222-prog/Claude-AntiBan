package cage

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestContainerMemorySetupNeverInvokesKernelOrSwapCommands(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is unavailable")
	}
	directory := t.TempDir()
	// Every host-changing operation in this script requires sudo. A container
	// detection must skip the entire block, including installation of services.
	for name, body := range map[string]string{"systemd-detect-virt": "#!/bin/sh\nexit 0\n", "sudo": "#!/bin/sh\necho unexpected-host-operation >&2\nexit 91\n"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(bash, "-e", "-s")
	cmd.Env = []string{"PATH=" + directory}
	cmd.Stdin = strings.NewReader(guestMemory)
	if output, err := cmd.CombinedOutput(); err != nil || len(output) > 0 {
		t.Fatalf("container touched kernel setup: %v %s", err, output)
	}
}
