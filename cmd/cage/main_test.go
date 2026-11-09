package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/cli"
)

func TestEntryDropsInheritedUnsafeSwitchesBeforeDispatch(t *testing.T) {
	// A benign early-return command exercises the entry boundary without a VM,
	// Git configuration changes, or access to the user's Cage profile.
	unsafe := []string{
		"AGENT_VM_UNSAFE_OPEN_NETWORK",
		"AGENT_VM_UNSAFE_WRITABLE_GIT",
		"AGENT_VM_UNSAFE_DISABLE_SECURITY_PROMPTS",
	}
	for _, name := range unsafe {
		t.Setenv(name, "1")
	}
	t.Setenv("CLAUDE_CAGE_CONFIG", "/not-accessed-by-help/config.json")
	previous := os.Args
	os.Args = []string{"cage", "help"}
	t.Cleanup(func() { os.Args = previous })
	if code := run(); code != 0 {
		t.Fatalf("help returned %d", code)
	}
	for _, name := range unsafe {
		if _, exists := os.LookupEnv(name); exists {
			t.Errorf("%s survived the Cage entry point", name)
		}
	}
	if got := os.Getenv("CLAUDE_CAGE_CONFIG"); got != "/not-accessed-by-help/config.json" {
		t.Errorf("profile selection changed: %q", got)
	}
}

func TestGuestServiceStartStreamsScriptAndPreservesInteractiveInput(t *testing.T) {
	script := cage.DefaultConfig().StartScript()
	t.Logf("service payload: %d bytes, %d UTF-16 code units", len(script), len(utf16.Encode([]rune(script))))
	if len(utf16.Encode([]rune(script))) <= 32767 {
		t.Fatal("fixture no longer exercises the Windows command-line limit")
	}
	input := strings.NewReader("interactive input must remain available")
	var output, stderr bytes.Buffer
	stdio := cli.IO{Stdin: input, Stdout: &output, Stderr: &stderr}
	code := runGuestServiceStart(script, stdio, func(args []string, streams cli.IO) int {
		if !slices.Equal(args, []string{"run", "bash", "-s"}) {
			t.Fatalf("unexpected service command: %q", args)
		}
		// Use a real child process to verify transport on Windows without
		// executing guest provisioning against the host filesystem.
		cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestGuestServiceScriptCaptureProcess$", "--"}, args...)...)
		cmd.Env = append(os.Environ(), "CAGE_TEST_SCRIPT_CAPTURE=1")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = streams.Stdin, streams.Stdout, streams.Stderr
		err := cmd.Run()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		if err != nil {
			t.Fatal(err)
		}
		return 0
	})
	if code != 17 || output.String() != fmt.Sprintf("%x", sha256.Sum256([]byte(script))) {
		t.Fatalf("script transport or exit status changed: code=%d stdout=%q stderr=%q", code, output.String(), stderr.String())
	}
	remaining, err := io.ReadAll(stdio.Stdin)
	if err != nil || string(remaining) != "interactive input must remain available" {
		t.Fatalf("internal service startup consumed interactive stdin: %q, %v", remaining, err)
	}
}

func TestGuestServiceScriptCaptureProcess(t *testing.T) {
	if os.Getenv("CAGE_TEST_SCRIPT_CAPTURE") != "1" {
		t.Skip("helper process for script transport")
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(18)
	}
	fmt.Printf("%x", sha256.Sum256(data))
	os.Exit(17)
}
