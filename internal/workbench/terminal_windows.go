package workbench

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ConPTY gives OpenSSH an actual Windows terminal. Pipes alone cannot deliver
// window resize, raw input, Ctrl+C, or full-screen programs correctly.
type windowsTerminal struct {
	input, output *os.File
	console       windows.Handle
	process       *os.Process
	once          sync.Once
	consoleOnce   sync.Once
	consoleMu     sync.Mutex
	done          chan struct{}
	waitErr       error
}

func startTerminal(cmd *exec.Cmd, cols, rows uint16) (terminalPTY, error) {
	inputRead, inputWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer inputRead.Close()
	outputRead, outputWrite, err := os.Pipe()
	if err != nil {
		inputWrite.Close()
		return nil, err
	}
	defer outputWrite.Close()
	p := &windowsTerminal{input: inputWrite, output: outputRead, done: make(chan struct{})}
	success := false
	defer func() {
		if !success {
			p.Close()
		}
	}()
	if err = windows.CreatePseudoConsole(windows.Coord{X: int16(cols), Y: int16(rows)}, windows.Handle(inputRead.Fd()), windows.Handle(outputWrite.Fd()), 0, &p.console); err != nil {
		return nil, err
	}
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, err
	}
	defer attrs.Delete()
	// This attribute takes the opaque HPCON value as lpValue, not a pointer
	// to it. Pass the integer handle to the syscall without forging a Go pointer.
	update := windows.NewLazySystemDLL("kernel32.dll").NewProc("UpdateProcThreadAttribute")
	if ok, _, callErr := update.Call(uintptr(unsafe.Pointer(attrs.List())), 0, windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, uintptr(p.console), unsafe.Sizeof(p.console), 0, 0); ok == 0 {
		return nil, callErr
	}
	startup := windows.StartupInfoEx{}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	// With redirected parent stdio, explicitly request fresh console handles.
	// Leaving this flag unset can retain the parent's pipes despite ConPTY.
	startup.Flags = windows.STARTF_USESTDHANDLES
	startup.ProcThreadAttributeList = attrs.List()
	app, err := windows.UTF16PtrFromString(cmd.Path)
	if err != nil {
		return nil, err
	}
	parts := make([]string, len(cmd.Args))
	for i, arg := range cmd.Args {
		parts[i] = syscall.EscapeArg(arg)
	}
	commandLine, err := windows.UTF16PtrFromString(strings.Join(parts, " "))
	if err != nil {
		return nil, err
	}
	var directory *uint16
	if cmd.Dir != "" {
		directory, err = windows.UTF16PtrFromString(cmd.Dir)
		if err != nil {
			return nil, err
		}
	}
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	for _, entry := range env {
		if strings.ContainsRune(entry, 0) {
			return nil, errors.New("invalid terminal environment")
		}
	}
	block := utf16.Encode([]rune(strings.Join(env, "\x00") + "\x00\x00"))
	var info windows.ProcessInformation
	err = windows.CreateProcess(app, commandLine, nil, nil, false, windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT, &block[0], directory, &startup.StartupInfo, &info)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(info.Thread)
	defer windows.CloseHandle(info.Process)
	p.process, err = os.FindProcess(int(info.ProcessId))
	if err != nil {
		windows.TerminateProcess(info.Process, 1)
		return nil, err
	}
	success = true
	go func() {
		_, p.waitErr = p.process.Wait()
		p.closeConsole()
		close(p.done)
	}()
	return p, nil
}
func (p *windowsTerminal) Read(b []byte) (int, error)  { return p.output.Read(b) }
func (p *windowsTerminal) Write(b []byte) (int, error) { return p.input.Write(b) }
func (p *windowsTerminal) Resize(cols, rows uint16) error {
	p.consoleMu.Lock()
	defer p.consoleMu.Unlock()
	if p.console == 0 {
		return os.ErrClosed
	}
	return windows.ResizePseudoConsole(p.console, windows.Coord{X: int16(cols), Y: int16(rows)})
}
func (p *windowsTerminal) Wait() error { <-p.done; return p.waitErr }
func (p *windowsTerminal) Kill() error { return p.process.Kill() }
func (p *windowsTerminal) closeConsole() {
	p.consoleOnce.Do(func() {
		p.consoleMu.Lock()
		defer p.consoleMu.Unlock()
		if p.console != 0 {
			windows.ClosePseudoConsole(p.console)
			p.console = 0
		}
	})
}
func (p *windowsTerminal) Close() error {
	p.once.Do(func() {
		p.input.Close()
		// Windows Server 2022 can wait for the final console output during
		// ClosePseudoConsole. Keep draining even when the session reader has
		// stopped; closing the output pipe first can deadlock console teardown.
		drained := make(chan struct{})
		go func() {
			_, _ = io.Copy(io.Discard, p.output)
			close(drained)
		}()
		p.closeConsole()
		p.output.Close()
		<-drained
	})
	return nil
}
