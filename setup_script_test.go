package agentvm

import (
	"os/exec"
	"runtime"
	"testing"
)

// tests/setup-script.sh: the setup script's blocks, run against stubs.
func TestSetupScript(t *testing.T) {
	runScript(t, "tests/setup-script.sh")
}

// tests/installer.sh: the curl installer and agent-vm.sh, against stub
// releases and a stub clone.
func TestInstaller(t *testing.T) {
	runScript(t, "tests/installer.sh")
}

func runScript(t *testing.T, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("runs in a Linux guest, or on macOS and Linux")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	out, err := exec.Command(bash, script).CombinedOutput()
	if err != nil {
		t.Errorf("%v\n%s", err, out)
	}
}
