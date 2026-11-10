//go:build !windows

package main

import (
	"github.com/creack/pty"
	ssh "github.com/gliderlabs/ssh"
	"io"
	"os/exec"
)

func runTerminal(s ssh.Session, cmd *exec.Cmd) int {
	p, wins, ok := s.Pty()
	if !ok {
		cmd.Stdin = s
		cmd.Stdout = s
		cmd.Stderr = s.Stderr()
		if err := cmd.Run(); err != nil {
			if x, ok := err.(*exec.ExitError); ok {
				return x.ExitCode()
			}
			return 1
		}
		return 0
	}
	file, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(p.Window.Width), Rows: uint16(p.Window.Height)})
	if err != nil {
		return 1
	}
	defer file.Close()
	resizeStop, resizeDone := make(chan struct{}), make(chan struct{})
	defer func() { close(resizeStop); <-resizeDone }()
	go func() {
		defer close(resizeDone)
		for {
			select {
			case <-resizeStop:
				return
			case <-s.Context().Done():
				_ = cmd.Process.Kill()
				return
			case w, ok := <-wins:
				if !ok {
					return
				}
				_ = pty.Setsize(file, &pty.Winsize{Cols: uint16(w.Width), Rows: uint16(w.Height)})
			}
		}
	}()
	go func() { _, _ = io.Copy(file, s) }()
	_, _ = io.Copy(s, file)
	if err := cmd.Wait(); err != nil {
		if x, ok := err.(*exec.ExitError); ok {
			return x.ExitCode()
		}
		return 1
	}
	return 0
}
