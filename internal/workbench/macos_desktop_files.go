package workbench

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path"
	"strings"
	"time"
)

func macOSFileFailure(reason string) error {
	if err := desktopDownloadFailure(reason); err != nil {
		return err
	}
	switch reason {
	case "requires_gui_login":
		return errors.New("请先登录并解锁虚拟 Mac 的普通账户")
	case "accessibility_permission":
		return errors.New("请在虚拟 Mac 中允许 Cage Guest Window 使用辅助功能，再重新传递文件")
	case "window_changed", "file_dialog_changed", "gui_session_changed", "attachment_cancelled":
		return errors.New("接收文件的 Claude 窗口已变化，请回到原窗口重新选择文件")
	case "file_dialog_navigation_failed", "file_dialog_still_open", "file_dialog_selection_mismatch":
		return errors.New("客机文件选择框尚未接收文件，请检查该选择框后重试")
	case "attachment_incomplete":
		return errors.New("附件尚未完整传入，请重新选择文件")
	case "attachment_limit":
		return errors.New("一次最多传递 100 个附件，请分批选择文件")
	default:
		return errors.New("虚拟 Mac 未能接收附件，请确认 Claude Desktop 已打开，且客机组件已更新")
	}
}

func (s *Server) macOSDesktopFileCall(ctx context.Context, input macOSWindowRequest) (map[string]any, error) {
	if input.Action == "files-start" && (input.Window == 0 || input.PID <= 0) {
		return nil, macOSFileFailure("window_changed")
	}
	data, err := s.macOSWindowCall(ctx, input)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if json.Unmarshal(data, &result) != nil {
		return nil, errors.New("虚拟 Mac 附件响应无效")
	}
	if reason, _ := result["error"].(string); reason != "" {
		return nil, macOSFileFailure(reason)
	}
	return result, nil
}

func validMacOSClipboardAttachment(value, name string) bool {
	if path.Clean(value) != value || strings.ContainsAny(value, "\\\x00\r\n") || path.Base(value) != name {
		return false
	}
	if !(strings.HasPrefix(value, "/var/folders/") || strings.HasPrefix(value, "/private/var/folders/") || strings.HasPrefix(value, "/tmp/") || strings.HasPrefix(value, "/private/tmp/")) {
		return false
	}
	directory := path.Base(path.Dir(value))
	suffix, ok := strings.CutPrefix(directory, "cage-clipboard-")
	return ok && suffix != "" && strings.Trim(suffix, "0123456789") == ""
}

func (s *Server) macOSUploadDesktopAttachment(ctx context.Context, name, id string, body io.Reader) (map[string]any, error) {
	if _, err := s.macOSDesktopFileCall(ctx, macOSWindowRequest{Action: "files-validate", ID: id}); err != nil {
		return nil, err
	}
	cmd, _, err := s.clipboardCommand(ctx, "upload", clipboardFileSelection{})
	if err != nil {
		return nil, err
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		input.Close()
		output.Close()
		return nil, err
	}
	finished := false
	defer func() {
		input.Close()
		output.Close()
		if !finished {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	if err = json.NewEncoder(input).Encode(map[string]string{"name": name}); err == nil {
		err = writeWindowsAttachmentFrames(input, body)
	}
	if err != nil {
		return nil, err
	}
	input.Close()
	var reply struct {
		Paths []string `json:"paths"`
	}
	reader := bufio.NewScanner(output)
	reader.Buffer(make([]byte, 4096), 65536)
	if !reader.Scan() || json.Unmarshal(reader.Bytes(), &reply) != nil || len(reply.Paths) != 1 || !validMacOSClipboardAttachment(reply.Paths[0], name) {
		return nil, errors.New("虚拟 Mac 附件没有完整保存，请重新传递")
	}
	err = cmd.Wait()
	finished = true
	if err != nil {
		return nil, errors.New("虚拟 Mac 附件传递中断，请重新选择文件")
	}
	if _, err = s.macOSDesktopFileCall(ctx, macOSWindowRequest{Action: "files-add", ID: id, Files: reply.Paths}); err != nil {
		// An upload may finish after its window or batch was canceled. Remove
		// only the private file just returned by this successful upload.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		request, _ := json.Marshal(map[string]any{"operation": "discard", "files": reply.Paths})
		command := s.sshCommand(cleanup, "-T", s.sshTarget(), "--", "exec '/Applications/Cage Guest Clipboard.app/Contents/MacOS/CageGuestClipboard' --client request")
		command.Stdin = bytes.NewReader(append(request, '\n'))
		command.Stdout, command.Stderr = io.Discard, io.Discard
		_ = command.Run()
		return nil, err
	}
	return map[string]any{"ok": true, "id": id, "name": name}, nil
}

func (s *Server) macOSPasteDesktopImage(ctx context.Context, data []byte, window macOSWindowRequest) (map[string]any, error) {
	if len(data) == 0 || len(data) > maxImageBytes {
		return nil, errors.New("图片大小无效")
	}
	if window.Window == 0 || window.PID <= 0 {
		return nil, macOSFileFailure("window_changed")
	}
	header, _ := json.Marshal(map[string]any{"action": "paste-image", "length": len(data), "pid": window.PID, "window": window.Window})
	cmd := s.sshCommand(ctx, "-T", s.sshTarget(), "--", macOSWindowSocketCommand)
	cmd.Stdin = io.MultiReader(bytes.NewReader(append(header, '\n')), bytes.NewReader(data))
	cmd.Stderr = io.Discard
	var reply bytes.Buffer
	cmd.Stdout = &boundedWindowsStatusWriter{buffer: &reply, remaining: 65536}
	if cmd.Run() != nil {
		return nil, errors.New("图片未能传入虚拟 Mac，请确认 Claude Desktop 已打开")
	}
	var result map[string]any
	if json.Unmarshal(reply.Bytes(), &result) != nil {
		return nil, errors.New("虚拟 Mac 图片响应无效")
	}
	if reason, _ := result["error"].(string); reason != "" {
		return nil, macOSFileFailure(reason)
	}
	if result["ok"] != true {
		return nil, errors.New("虚拟 Mac 未确认图片粘贴")
	}
	return result, nil
}
