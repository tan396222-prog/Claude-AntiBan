//go:build !windows

package workbench

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestUnixTerminalCloseReleasesPTYHeldByDescendant(t *testing.T) {
	var manager terminalManager
	t.Cleanup(manager.close)
	// A descendant retains the slave PTY after its parent exits. Some kernels
	// report EOF on the leader's exit; others retain the reader until closure.
	// Explicit closure must finish in either case without killing a PID group.
	cmd := exec.Command("/bin/sh", "-c", `trap '' HUP; sleep 60 & printf 'descendant:%s\n' "$!"; exit 0`)
	session, err := manager.start(cmd, "", "shell")
	if err != nil {
		t.Fatal(err)
	}
	sub := session.subscribe()
	defer sub.Close()
	text := readTerminalUntil(t, sub, "descendant:")
	// Read the complete PID line before registering cleanup of this test's
	// separately owned background process.
	for !strings.Contains(text[strings.Index(text, "descendant:"):], "\n") {
		text += readTerminalUntil(t, sub, "\n")
	}
	line := strings.Split(strings.SplitN(text, "descendant:", 2)[1], "\n")[0]
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || pid <= 0 {
		t.Fatalf("invalid descendant PID: %q", text)
	}
	child, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Kill(); _ = child.Release() })
	if err := session.file.Wait(); err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() { manager.close(); close(closed) }()
	awaitTerminalSignal(t, closed)
	awaitTerminalSignal(t, session.done)
	if manager.hasLive() {
		t.Fatal("explicit close left the terminal active")
	}
}
