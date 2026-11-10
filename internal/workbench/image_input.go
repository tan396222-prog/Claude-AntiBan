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

type imageInputRequest struct {
	PNG      string  `json:"png"`
	Text     *string `json:"text,omitempty"`
	Target   string  `json:"target"`
	ID       string  `json:"id"`
	Transfer string  `json:"transfer,omitempty"`
	Context  string  `json:"context,omitempty"`
	Window   uint32  `json:"window,omitempty"`
	PID      int32   `json:"pid,omitempty"`
}

func (s *Server) pasteInputStart(w http.ResponseWriter, r *http.Request) {
	var input imageInputRequest
	if !readJSON(w, r, &input) {
		return
	}
	if input.Target == "browser" && (input.ID == "" || len(input.ID) > 256) {
		http.Error(w, "请选择粘贴的网页", http.StatusBadRequest)
		return
	}
	if !s.fileTargetReady(input.Target, input.ID) {
		http.Error(w, "粘贴接收位置已关闭，请重新点击输入框", http.StatusConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if input.Target == "terminal" {
		jsonReply(w, map[string]any{"available": true, "context": input.ID})
		return
	}
	if input.Target == "desktop" {
		body, _ := json.Marshal(map[string]any{"kind": "image", "window": input.Window, "pid": input.PID})
		result, err := s.guestFiles(ctx, "desktop", "POST", "/start", bytes.NewReader(body), "")
		id, _ := result["id"].(string)
		if err != nil || !browserFileID.MatchString(id) {
			http.Error(w, "粘贴目标未能绑定，请重新点击输入框。旧客机需要先同步环境组件", http.StatusConflict)
			return
		}
		jsonReply(w, map[string]any{"available": true, "transfer": id})
		return
	}
	if s.macosGuest != nil || s.windowsGuest != nil {
		data, err := s.nativeBrowserClient().request(ctx, "POST", "/paste-start", map[string]any{"id": input.ID, "length": 0})
		var result struct {
			Transfer string `json:"transfer"`
		}
		if err != nil || json.Unmarshal(data, &result) != nil || !validInputToken(result.Transfer) {
			http.Error(w, "粘贴目标未能绑定，请重新点击输入框。旧客机需要先同步环境组件", http.StatusConflict)
			return
		}
		jsonReply(w, map[string]any{"available": true, "transfer": result.Transfer})
		return
	}
	data, err := s.browserAction(ctx, "ime", &browserInput{Target: "browser", ID: input.ID, Mode: "status"})
	var state struct {
		Available bool   `json:"available"`
		Context   string `json:"context"`
	}
	if err != nil || json.Unmarshal(data, &state) != nil || !state.Available || !validInputToken(state.Context) {
		http.Error(w, "图片接收位置尚未聚焦，请点击输入框后重试", http.StatusConflict)
		return
	}
	jsonReply(w, map[string]any{"available": true, "context": state.Context})
}

func (s *Server) pasteInputCancel(w http.ResponseWriter, r *http.Request) {
	var input imageInputRequest
	if !readJSON(w, r, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if input.Target == "desktop" && browserFileID.MatchString(input.Transfer) {
		body, _ := json.Marshal(map[string]string{"id": input.Transfer})
		_, _ = s.guestFiles(ctx, "desktop", "POST", "/cancel", bytes.NewReader(body), "")
	} else if input.Target == "browser" && validInputToken(input.Transfer) && (s.macosGuest != nil || s.windowsGuest != nil) {
		_, _ = s.nativeBrowserClient().request(ctx, "POST", "/paste-cancel", map[string]string{"transfer": input.Transfer})
	}
	jsonReply(w, map[string]bool{"ok": true})
}

func (s *Server) textInput(w http.ResponseWriter, r *http.Request) {
	var input imageInputRequest
	if !readJSON(w, r, &input, 6*(2<<20)+4096) {
		return
	}
	if input.Target != "browser" || input.ID == "" || len(input.ID) > 256 || input.Text == nil || len(*input.Text) > 2<<20 {
		http.Error(w, "粘贴文字或目标无效，文本最多 2 MiB", http.StatusBadRequest)
		return
	}
	if !s.fileTargetReady("browser", input.ID) {
		http.Error(w, "网页已关闭或环境尚未就绪", http.StatusConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if *input.Text == "" {
		jsonReply(w, map[string]bool{"ok": true})
		return
	}
	var err error
	if s.macosGuest != nil || s.windowsGuest != nil {
		if !validInputToken(input.Transfer) {
			http.Error(w, "粘贴目标尚未绑定，请重新点击输入框", http.StatusConflict)
			return
		}
		_, err = s.nativeBrowserClient().pasteData(ctx, input.ID, []byte(*input.Text), input.Transfer, "text/plain")
	} else {
		if !validInputToken(input.Context) {
			http.Error(w, "粘贴目标尚未绑定，请重新点击输入框", http.StatusConflict)
			return
		}
		body, _ := json.Marshal(map[string]string{"id": input.ID, "text": *input.Text, "context": input.Context})
		s.mu.Lock()
		token := s.credentials.Token
		s.mu.Unlock()
		_, err = s.guestAPI(ctx, token, "POST", "/paste-text", body)
	}
	if err != nil {
		http.Error(w, "文字未能交给原输入位置，请核对页面与客机组件后重试", http.StatusConflict)
		return
	}
	jsonReply(w, map[string]bool{"ok": true})
}

func decodeInputImage(encoded string) ([]byte, error) {
	if len(encoded) > base64.StdEncoding.EncodedLen(maxImageBytes) {
		return nil, errors.New("图片不能超过 20 MB")
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(data) == 0 || len(data) > maxImageBytes {
		return nil, errors.New("图片数据无效")
	}
	return validateInputImage(data)
}

func validateInputImage(data []byte) ([]byte, error) {
	if len(data) == 0 || len(data) > maxImageBytes {
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
	var input imageInputRequest
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
		if input.Context != "" && s.macosGuest == nil && s.windowsGuest == nil {
			state, checkErr := s.browserAction(ctx, "ime", &browserInput{Target: "browser", ID: input.ID, Mode: "status"})
			var current struct {
				Available bool   `json:"available"`
				Context   string `json:"context"`
			}
			if checkErr != nil || json.Unmarshal(state, &current) != nil || !current.Available || current.Context != input.Context {
				http.Error(w, "图片接收位置已变化，请重新粘贴", http.StatusConflict)
				return
			}
		}
		body, _ := json.Marshal(map[string]string{"id": input.ID, "png": input.PNG, "transfer": input.Transfer})
		if _, err = s.guestAPI(ctx, token, "POST", "/paste-image", body); err != nil {
			http.Error(w, "图片未能粘贴，请点击支持图片的输入框后重试；环境组件过旧时请先更新环境", 502)
			return
		}
		jsonReply(w, map[string]bool{"ok": true})
		return
	}
	if input.Target == "desktop" {
		if !browserFileID.MatchString(input.Transfer) {
			http.Error(w, "图片目标尚未绑定，请重新点击输入框后粘贴", http.StatusConflict)
			return
		}
		if _, err = s.guestFiles(ctx, "desktop", "POST", "/upload?id="+input.Transfer, bytes.NewReader(data), "image.png"); err == nil {
			body, _ := json.Marshal(map[string]string{"id": input.Transfer})
			var result map[string]any
			result, err = s.guestFiles(ctx, "desktop", "POST", "/finish-image", bytes.NewReader(body), "")
			if err == nil {
				jsonReply(w, result)
				return
			}
		}
		http.Error(w, "图片未能交给原输入位置，请核对窗口和客机组件后重新粘贴", http.StatusConflict)
		return
	}
	result, err := s.guestImage(ctx, input.Target, data, macOSWindowRequest{Window: input.Window, PID: input.PID})
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	jsonReply(w, result)
}

func (s *Server) guestImage(ctx context.Context, target string, data []byte, window macOSWindowRequest) (map[string]any, error) {
	if s.macosGuest != nil && target == "desktop" {
		return s.macOSPasteDesktopImage(ctx, data, window)
	}
	if s.macosGuest != nil && target == "terminal" {
		result, err := s.guestFiles(ctx, target, "POST", "/upload", bytes.NewReader(data), "image.png")
		if err != nil {
			return nil, err
		}
		path, _ := result["path"].(string)
		if !s.validMacOSAttachmentPath(path, "image.png") {
			return nil, errors.New("图片保存路径无效")
		}
		return result, nil
	}
	if s.windowsGuest != nil {
		if target == "terminal" {
			return s.windowsSaveAttachment(ctx, "image.png", bytes.NewReader(data))
		}
		var result map[string]any
		err := s.windowsDesktopRequest(ctx, "paste-image", map[string]any{"png": base64.StdEncoding.EncodeToString(data)}, &result)
		return result, err
	}
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
