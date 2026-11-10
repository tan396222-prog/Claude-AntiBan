//go:build windows

package workbench

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// This exercises actual Windows process ownership without opening a VM or a
// network connection. Killing the owned helper must also retire its relay.
func TestWindowsNetworkProcessTree(t *testing.T) {
	mode := os.Getenv("CAGE_TEST_WINDOWS_NETWORK_PROCESS_MODE")
	if mode == "leaf" {
		time.Sleep(5 * time.Minute)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if mode == "helper" {
		if err := containWindowsNetworkProcess(); err != nil {
			t.Fatal(err)
		}
		child := exec.Command(executable, "-test.run=^TestWindowsNetworkProcessTree$")
		child.Env = append(os.Environ(), "CAGE_TEST_WINDOWS_NETWORK_PROCESS_MODE=leaf")
		child.Stdout, child.Stderr = io.Discard, io.Discard
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(os.Stdout, child.Process.Pid)
		time.Sleep(5 * time.Minute)
		return
	}
	if os.Getenv("CAGE_TEST_WINDOWS_NETWORK_PROCESS_TREE") != "1" {
		t.Skip("requires explicitly enabled native process-tree QA")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	helper := exec.CommandContext(ctx, executable, "-test.run=^TestWindowsNetworkProcessTree$")
	helper.Env = append(os.Environ(), "CAGE_TEST_WINDOWS_NETWORK_PROCESS_MODE=helper")
	helper.Stderr = os.Stderr
	output, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	defer helper.Process.Kill()
	line, err := bufio.NewReader(output).ReadString('\n')
	id, parseErr := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || parseErr != nil || id <= 0 || !processExists(id) {
		t.Fatal("owned relay process was not observed")
	}
	leaf, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(id))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(leaf)
	defer windows.TerminateProcess(leaf, 1)
	if err := helper.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = helper.Wait()
	deadline := time.Now().Add(5 * time.Second)
	var code uint32
	live := func() bool { return windows.GetExitCodeProcess(leaf, &code) != nil || code == 259 }
	for live() && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if live() {
		t.Fatal("owned relay survived its network helper")
	}
}
