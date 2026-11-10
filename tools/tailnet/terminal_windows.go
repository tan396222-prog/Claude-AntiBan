package main

import (
	"github.com/UserExistsError/conpty"
	ssh "github.com/gliderlabs/ssh"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
)

func runTerminal(s ssh.Session, cmd *exec.Cmd) int {
	p, wins, ok := s.Pty()
	if !ok {
		cmd.Stdin = s
		cmd.Stdout = s
		cmd.Stderr = s.Stderr()
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := cmd.Run(); err != nil {
			if x, ok := err.(*exec.ExitError); ok {
				return x.ExitCode()
			}
			return 1
		}
		return 0
	}
	args := make([]string, len(cmd.Args))
	for i, arg := range cmd.Args {
		args[i] = syscall.EscapeArg(arg)
	}
	terminal, err := conpty.Start(strings.Join(args, " "), conpty.ConPtyDimensions(p.Window.Width, p.Window.Height), conpty.ConPtyWorkDir(cmd.Dir))
	if err != nil {
		return 1
	}
	var closeOnce sync.Once
	var consoleMu sync.Mutex
	closeTerminal := func() { closeOnce.Do(func() { consoleMu.Lock(); defer consoleMu.Unlock(); _ = terminal.Close() }) }
	defer closeTerminal()
	resizeStop, resizeDone := make(chan struct{}), make(chan struct{})
	defer func() { close(resizeStop); <-resizeDone }()
	go func() {
		defer close(resizeDone)
		for {
			select {
			case <-resizeStop:
				return
			case <-s.Context().Done():
				closeTerminal()
				return
			case w, ok := <-wins:
				if !ok {
					return
				}
				consoleMu.Lock()
				_ = terminal.Resize(w.Width, w.Height)
				consoleMu.Unlock()
			}
		}
	}()
	go func() { _, _ = io.Copy(terminal, s) }()
	go func() { _, _ = io.Copy(s, terminal) }()
	code, err := terminal.Wait(s.Context())
	if err != nil {
		return 1
	}
	return int(code)
}
