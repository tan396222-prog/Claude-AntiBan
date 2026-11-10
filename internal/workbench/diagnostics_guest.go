package workbench

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"al.essio.dev/pkg/shellescape"
	"github.com/sylvinus/agent-vm/internal/guestos"
)

//go:embed macos_diagnostics.py
var macOSDiagnosticCollector string

func (s *Server) nativeGuestDiagnostics(ctx context.Context) ([]byte, error) {
	var data []byte
	var err error
	if s.windowsGuest != nil {
		cmd, err := guestos.WindowsRelayCommand(ctx, s.profile, "diagnostics")
		if err != nil {
			return nil, err
		}
		cmd.Stdin = strings.NewReader("{\"action\":\"read\"}\n")
		pipe, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		data, err = io.ReadAll(io.LimitReader(pipe, (64<<10)+1))
		if err != nil || len(data) > 64<<10 {
			_ = cmd.Process.Kill()
		}
		err = errors.Join(err, cmd.Wait())
		if err != nil || len(data) > 64<<10 {
			return nil, errors.New("Windows 客机诊断记录未完整返回")
		}
	} else {
		command := "exec " + shellescape.QuoteCommand([]string{macCLIInstall + "/bin/python3", "-I", "-c", macOSDiagnosticCollector})
		data, err = s.guestControl(ctx, command, "guest-diagnostics")
		if err != nil {
			return nil, err
		}
	}
	var result struct {
		Version int    `json:"version"`
		Lines   string `json:"lines"`
	}
	if json.Unmarshal(data, &result) != nil || result.Version != 1 || len(result.Lines) > 48<<10 || strings.Count(result.Lines, "\n") > 150 {
		return nil, errors.New("客机诊断记录格式或大小无效")
	}
	return []byte(result.Lines), nil
}
