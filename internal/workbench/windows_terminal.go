package workbench

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// windowsGuestTerminal transports the GUI user's guest ConPTY. A host ConPTY
// would apply a second terminal parser and could not resize the guest console.
type windowsGuestTerminal struct {
	cmd     *exec.Cmd
	input   io.WriteCloser
	output  *io.PipeReader
	writeMu sync.Mutex
	once    sync.Once
	done    chan struct{}
	waitErr error
}

func startWindowsGuestTerminal(cmd *exec.Cmd, cols, rows uint16) (terminalPTY, error) {
	return startWindowsGuestTerminalAt(cmd, cols, rows, nil)
}
func startWindowsGuestTerminalAt(cmd *exec.Cmd, cols, rows uint16, project *windowsProjectBinding) (terminalPTY, error) {
	open := map[string]any{"action": "open", "cols": cols, "rows": rows, "workspace": "home", "shell": "powershell"}
	if project != nil {
		open["workspace"] = "project"
		open["project_id"] = project.ID
		open["grant"] = project.Grant
	}
	return startWindowsGuestTerminalRequest(cmd, open)
}
func startWindowsGuestTerminalRequest(cmd *exec.Cmd, open any) (terminalPTY, error) {
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		input.Close()
		return nil, err
	}
	output, writer := io.Pipe()
	terminal := &windowsGuestTerminal{cmd: cmd, input: input, output: output, done: make(chan struct{})}
	// A missing guest agent cannot hold terminal admission indefinitely.
	deadlineDone := make(chan struct{})
	deadline := time.AfterFunc(15*time.Second, func() { defer close(deadlineDone); _ = cmd.Process.Kill(); _ = input.Close() })
	if err = terminal.send(open); err != nil {
		cancelWindowsAdmissionDeadline(deadline, deadlineDone)
		terminal.Close()
		_ = cmd.Wait()
		return nil, err
	}
	reader := bufio.NewScanner(stdout)
	reader.Buffer(make([]byte, 4096), 100<<10)
	var ready struct {
		Ready bool `json:"ready"`
	}
	if !reader.Scan() || json.Unmarshal(reader.Bytes(), &ready) != nil || !ready.Ready {
		cancelWindowsAdmissionDeadline(deadline, deadlineDone)
		terminal.Close()
		_ = cmd.Wait()
		return nil, errors.New("Windows 客机终端未就绪")
	}
	if !cancelWindowsAdmissionDeadline(deadline, deadlineDone) {
		terminal.Close()
		_ = cmd.Wait()
		return nil, errors.New("Windows 客机终端连接超时")
	}
	go func() {
		var failure error
		sawExit := false
		for reader.Scan() {
			var message struct {
				Data  *string `json:"data"`
				Exit  *int    `json:"exit"`
				Error string  `json:"error"`
			}
			if json.Unmarshal(reader.Bytes(), &message) != nil {
				failure = errors.New("Windows 客机终端响应无效")
				break
			}
			if message.Error != "" {
				failure = errors.New("Windows 客机终端不可用")
				break
			}
			if message.Exit != nil {
				sawExit = true
				if *message.Exit != 0 {
					failure = errors.New("Windows 客机终端进程已退出")
				}
				break
			}
			if message.Data == nil {
				failure = errors.New("Windows 客机终端响应无效")
				break
			}
			data, err := base64.StdEncoding.DecodeString(*message.Data)
			if err != nil || len(data) > 64<<10 {
				failure = errors.New("Windows 客机终端输出无效")
				break
			}
			if _, err = writer.Write(data); err != nil {
				failure = err
				break
			}
		}
		if !sawExit && failure == nil {
			failure = errors.New("Windows 客机终端连接已中断")
		}
		if scanErr := reader.Err(); scanErr != nil && failure == nil {
			failure = scanErr
		}
		// Reap even if the relay has lost its output without closing the process.
		_ = input.Close()
		if !sawExit {
			_ = cmd.Process.Kill()
		}
		processErr := cmd.Wait()
		if failure == nil {
			failure = processErr
		}
		terminal.waitErr = failure
		_ = writer.CloseWithError(failure)
		close(terminal.done)
	}()
	return terminal, nil
}
func (t *windowsGuestTerminal) send(value any) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	return t.sendLocked(value)
}

func (t *windowsGuestTerminal) sendLocked(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	for len(data) > 0 {
		n, err := t.input.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
func (t *windowsGuestTerminal) Read(data []byte) (int, error) { return t.output.Read(data) }
func (t *windowsGuestTerminal) Write(data []byte) (int, error) {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	written := 0
	for written < len(data) {
		end := min(written+(64<<10), len(data))
		if err := t.sendLocked(map[string]any{"action": "input", "data": base64.StdEncoding.EncodeToString(data[written:end])}); err != nil {
			return written, err
		}
		written = end
	}
	return written, nil
}
func (t *windowsGuestTerminal) Resize(cols, rows uint16) error {
	if cols < 1 || cols >= 600 || rows < 1 || rows > 500 {
		return errors.New("终端尺寸无效")
	}
	return t.send(map[string]any{"action": "resize", "cols": cols, "rows": rows})
}
func (t *windowsGuestTerminal) Wait() error { <-t.done; return t.waitErr }
func (t *windowsGuestTerminal) Kill() error {
	if t.cmd.Process == nil {
		return os.ErrProcessDone
	}
	return t.cmd.Process.Kill()
}
func (t *windowsGuestTerminal) Close() error {
	t.once.Do(func() { _ = t.input.Close(); _ = t.output.Close(); _ = t.Kill() })
	return nil
}
