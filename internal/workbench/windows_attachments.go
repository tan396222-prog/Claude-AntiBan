package workbench

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/sylvinus/agent-vm/internal/guestos"
)

func validWindowsAttachmentPath(path, name string) bool {
	if len(path) < 3 || len(path) > 32767 || path[1] != ':' || path[2] != '\\' || !((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) || strings.ContainsAny(path[2:], ":/\x00\r\n") {
		return false
	}
	parts := strings.Split(path[3:], `\`)
	root := len(parts) - 4
	if root >= 1 && parts[root] != "Cage" && parts[root-1] == "Cage" && parts[root] == "Attachments" {
		root-- // Chooser batches give each file its own private child directory.
	}
	if root < 0 || parts[root] != "Cage" || parts[root+1] != "Attachments" || parts[len(parts)-1] != name {
		return false
	}
	for _, id := range parts[root+2 : len(parts)-1] {
		if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" {
			return false
		}
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func (s *Server) windowsSaveAttachment(ctx context.Context, name string, body io.Reader) (map[string]any, error) {
	return s.windowsUploadAttachment(ctx, name, "", body)
}

func (s *Server) windowsUploadAttachment(ctx context.Context, name, id string, body io.Reader) (map[string]any, error) {
	cmd, err := guestos.WindowsRelayCommand(ctx, s.profile, "attachments")
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
	done := false
	defer func() {
		input.Close()
		output.Close()
		if !done {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	request := map[string]string{"action": "save", "name": name}
	if id != "" {
		request["id"] = id
	}
	if err = json.NewEncoder(input).Encode(request); err == nil {
		err = writeWindowsAttachmentFrames(input, body)
	}
	if err != nil {
		return nil, err
	}
	input.Close()
	var result map[string]any
	reader := bufio.NewScanner(output)
	reader.Buffer(make([]byte, 4096), 65536)
	if !reader.Scan() || json.Unmarshal(reader.Bytes(), &result) != nil {
		return nil, errors.New("Windows 附件没有完整保存，请重新传递")
	}
	err = cmd.Wait()
	done = true
	path, _ := result["path"].(string)
	if err != nil || result["ok"] != true || result["scope"] != "attachments" || !validWindowsAttachmentPath(path, name) {
		return nil, errors.New("Windows 附件保存失败，请确认客机仍已解锁，且文件名有效")
	}
	if id != "" {
		// Desktop batches are completed by the guest broker. Their paths are
		// private to that broker and never needed by a host file chooser.
		public := map[string]any{"ok": true, "id": id, "name": name}
		if raw, exists := result["size"]; exists {
			size, ok := raw.(float64)
			if !ok || size < 0 || size > float64(browserFileLimit) || size != float64(int64(size)) {
				return nil, errors.New("Windows 附件保存结果无效")
			}
			public["size"] = size
		}
		return public, nil
	}
	return result, nil
}

func writeWindowsAttachmentFrames(output io.Writer, input io.Reader) error {
	buffer := make([]byte, 64<<10)
	header := make([]byte, 4)
	var count int64
	for {
		n, err := input.Read(buffer)
		count += int64(n)
		if count > browserFileLimit {
			return errors.New("每个文件不能超过 2 GB")
		}
		if n > 0 {
			binary.LittleEndian.PutUint32(header, uint32(n))
			if writeErr := writeWindowsAttachmentPart(output, header); writeErr != nil {
				return writeErr
			}
			if writeErr := writeWindowsAttachmentPart(output, buffer[:n]); writeErr != nil {
				return writeErr
			}
		}
		if err != nil {
			if err != io.EOF {
				return err
			}
			break
		}
	}
	return writeWindowsAttachmentPart(output, []byte{0, 0, 0, 0})
}

func writeWindowsAttachmentPart(output io.Writer, data []byte) error {
	for len(data) != 0 {
		n, err := output.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(data) {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
