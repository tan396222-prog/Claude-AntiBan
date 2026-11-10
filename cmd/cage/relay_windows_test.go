package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestRelayChildHasNoConsoleAndCapturesOutput(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestRelayConsoleChild$")
	cmd.Env = append(os.Environ(), "CAGE_RELAY_CONSOLE_TEST=1")
	configureRelayCommand(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "console=0") || !strings.Contains(string(output), "stderr-captured") {
		t.Fatalf("relay child: %s, %v", output, err)
	}
}

func TestRelayConsoleChild(t *testing.T) {
	if os.Getenv("CAGE_RELAY_CONSOLE_TEST") != "1" {
		return
	}
	window, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow").Call()
	fmt.Printf("console=%d\n", window)
	fmt.Fprintln(os.Stderr, "stderr-captured")
}
