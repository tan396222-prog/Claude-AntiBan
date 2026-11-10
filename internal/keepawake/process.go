//go:build darwin || linux

package keepawake

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// The child acknowledges startup only after the inhibitor utility launches it.
// Closing stdin ends the shell's builtin read, so no orphaned sleep process or
// permanent system setting remains after the hosting service exits.
const waitForHost = `printf 'ready\n'; read -r cage_remote_wait`

func holdProcess(ctx context.Context, ready func(), command string, args ...string) error {
	cmd := exec.Command(command, args...)
	cmd.WaitDelay = 2 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(done) }()
	finish := func() {
		_ = stdin.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}
	defer finish()
	ack := make(chan bool, 1)
	go func() { line, err := bufio.NewReader(stdout).ReadString('\n'); ack <- err == nil && line == "ready\n" }()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errors.New("主机防休眠请求超时")
	case ok := <-ack:
		if !ok {
			finish()
			message := strings.TrimSpace(stderr.String())
			if len(message) > 512 {
				message = message[:512]
			}
			return errors.New("无法启用主机防休眠：" + message)
		}
	}
	ready()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return waitErr
	}
}
