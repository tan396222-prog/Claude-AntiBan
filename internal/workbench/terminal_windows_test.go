package workbench

import (
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestWindowsTerminalInputResizeAndExit(t *testing.T) {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "$v=[Console]::ReadLine(); [Console]::WriteLine('received:'+$v)")
	terminal, err := startTerminal(cmd, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = terminal.Kill(); _ = terminal.Close() })
	if err = terminal.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	output := make(chan string, 1)
	go func() { b, _ := io.ReadAll(terminal); output <- string(b) }()
	if _, err = io.WriteString(terminal, "cage-terminal\r"); err != nil {
		t.Fatal(err)
	}
	select {
	case text := <-output:
		if !strings.Contains(text, "received:cage-terminal") {
			t.Fatalf("ConPTY input did not reach child: %q", text)
		}
		if err := terminal.Wait(); err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("ConPTY did not drain and close after child exit")
	}
}
