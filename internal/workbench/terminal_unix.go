//go:build !windows

package workbench

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

type unixTerminal struct {
	*os.File
	cmd     *exec.Cmd
	done    chan struct{}
	waitErr error
}

func startTerminal(cmd *exec.Cmd, cols, rows uint16) (terminalPTY, error) {
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, err
	}
	// Register a nonblocking master with Go's poller so Close interrupts both
	// reads and writes, including a full input queue during service shutdown.
	fd, err := unix.FcntlInt(f.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err == nil {
		err = syscall.SetNonblock(fd, true)
	}
	if err != nil {
		if fd >= 0 {
			_ = syscall.Close(fd)
		}
		_ = cmd.Process.Kill()
		_ = f.Close()
		_ = cmd.Wait()
		return nil, err
	}
	master := os.NewFile(uintptr(fd), f.Name())
	_ = f.Close()
	p := &unixTerminal{File: master, cmd: cmd, done: make(chan struct{})}
	go func() {
		p.waitErr = cmd.Wait()
		close(p.done)
	}()
	return p, nil
}
func (p *unixTerminal) Resize(cols, rows uint16) error {
	// File.Fd switches a pollable descriptor back to blocking mode. Use the
	// raw descriptor only within Control so resize preserves cancellable I/O.
	raw, err := p.File.SyscallConn()
	if err != nil {
		return err
	}
	var resizeErr error
	if err := raw.Control(func(fd uintptr) {
		resizeErr = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &unix.Winsize{Col: cols, Row: rows})
	}); err != nil {
		return err
	}
	return resizeErr
}
func (p *unixTerminal) Wait() error { <-p.done; return p.waitErr }
func (p *unixTerminal) Kill() error { return p.cmd.Process.Kill() }
