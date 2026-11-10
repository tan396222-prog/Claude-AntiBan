package keepawake

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMacNativePowerRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- hold(ctx, func() { close(ready) }) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatal("power request failed", err)
	case <-time.After(6 * time.Second):
		t.Fatal("power request did not start")
	}
	owned := func(pid int) bool {
		for i := 0; i < 8 && pid > 1; i++ {
			if pid == os.Getpid() {
				return true
			}
			value, err := exec.Command("/bin/ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
			if err != nil {
				return false
			}
			pid, _ = strconv.Atoi(strings.TrimSpace(string(value)))
		}
		return false
	}
	pattern := regexp.MustCompile(`pid ([0-9]+)\(caffeinate\):[^\n]+`)
	var assertionPID string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && assertionPID == "" {
		data, err := exec.Command("/usr/bin/pmset", "-g", "assertions").Output()
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range pattern.FindAllStringSubmatch(string(data), -1) {
			pid, _ := strconv.Atoi(match[1])
			if owned(pid) {
				if !strings.Contains(match[0], "PreventUserIdleSystemSleep") || strings.Contains(match[0], "PreventUserIdleDisplaySleep") {
					t.Fatal("wrong assertion", match[0])
				}
				assertionPID = match[1]
			}
		}
		if assertionPID == "" {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if assertionPID == "" {
		t.Fatal("no owned idle-system-sleep assertion found")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("power helper did not exit")
	}
	data, err := exec.Command("/usr/bin/pmset", "-g", "assertions").Output()
	if err != nil || strings.Contains(string(data), "pid "+assertionPID+"(caffeinate):") {
		t.Fatal("power assertion survived cancellation", err)
	}
}
