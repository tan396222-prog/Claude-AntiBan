package workbench

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/png"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxImageBytes = 20 << 20

func decodeInputImage(encoded string) ([]byte, error) {
	if len(encoded) > base64.StdEncoding.EncodedLen(maxImageBytes) {
		return nil, errors.New("图片不能超过 20 MB")
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(data) == 0 || len(data) > maxImageBytes {
		return nil, errors.New("图片数据无效")
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 40_000_000 {
		return nil, errors.New("需要有效的 PNG 图片，且不能超过 4000 万像素")
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return nil, errors.New("图片已损坏，请重新复制或选择图片")
	}
	return data, nil
}

func (s *Server) imageInput(w http.ResponseWriter, r *http.Request) {
	var input struct {
		PNG    string `json:"png"`
		Target string `json:"target"`
		ID     string `json:"id"`
	}
	if !readJSON(w, r, &input, int64(base64.StdEncoding.EncodedLen(maxImageBytes)+4096)) {
		return
	}
	if (input.Target != "browser" && input.Target != "desktop" && input.Target != "terminal") || (input.Target != "desktop" && input.ID == "") || len(input.ID) > 256 {
		http.Error(w, "图片目标无效", 400)
		return
	}
	s.mu.Lock()
	ready, token, terminal := s.ready, s.credentials.Token, s.terminals.get(input.ID)
	s.mu.Unlock()
	if !ready {
		http.Error(w, "环境尚未就绪", 409)
		return
	}
	if input.Target == "terminal" {
		if terminal == nil {
			http.Error(w, "终端已关闭", 409)
			return
		}
		terminal.mu.Lock()
		closed := terminal.closed
		terminal.mu.Unlock()
		if closed {
			http.Error(w, "终端已关闭", 409)
			return
		}
	}
	data, err := decodeInputImage(input.PNG)
	if err != nil {
		requestError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if input.Target == "browser" {
		body, _ := json.Marshal(map[string]string{"id": input.ID, "png": input.PNG})
		if _, err = s.guestAPI(ctx, token, "POST", "/paste-image", body); err != nil {
			http.Error(w, "图片未能粘贴，请点击支持图片的输入框后重试；环境组件过旧时请先更新环境", 502)
			return
		}
		jsonReply(w, map[string]bool{"ok": true})
		return
	}
	result, err := s.guestImage(ctx, input.Target, data)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	jsonReply(w, result)
}

func (s *Server) guestImage(ctx context.Context, target string, data []byte) (map[string]any, error) {
	script := "sudo -H -u cage /usr/local/bin/cage-image-input save"
	if target == "desktop" {
		script = "systemctl is-active --quiet cage-claude-desktop.service && sudo -H -u cage env DISPLAY=:78 XAUTHORITY=/run/cage-desktop/Xauthority /usr/local/bin/cage-image-input paste"
	}
	cmd := s.sshCommand(ctx, "-T", s.sshTarget(), "--", script)
	cmd.Stdin = bytes.NewReader(data)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, errors.New("无法连接 Cage 环境")
	}
	output, readErr := io.ReadAll(io.LimitReader(pipe, 4097))
	if readErr != nil || len(output) > 4096 {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, errors.New("图片传递响应无效")
	}
	if err = cmd.Wait(); err != nil {
		return nil, errors.New("图片未能传入 Cage，请确认目标已打开并支持图片；环境组件过旧时请先更新环境")
	}
	var result map[string]any
	if json.Unmarshal(output, &result) != nil {
		return nil, errors.New("图片传递响应无效")
	}
	if target == "terminal" {
		path, ok := result["path"].(string)
		if !ok || !strings.HasPrefix(path, "/home/cage/.local/share/cage/attachments/image-") || strings.ContainsAny(path, "'\r\n\x00") {
			return nil, errors.New("图片保存路径无效")
		}
	}
	return result, nil
}
