package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"al.essio.dev/pkg/shellescape"
)

var terminalFileID = regexp.MustCompile(`^[a-f0-9]{16}$`)

func validFileTargetID(target, id string) bool {
	if target == "terminal" {
		return terminalFileID.MatchString(id)
	}
	return browserFileID.MatchString(id)
}

// Desktop requests use the private session socket, carried over the existing
// SSH connection. Directory selections use an authorized project's guest path.
func (s *Server) guestFiles(ctx context.Context, target, method, path string, body io.Reader, name string) (map[string]any, error) {
	script := "sudo -H -u cage curl --silent --show-error --fail --max-time 1800 --unix-socket /run/cage-desktop/files.sock -X " + method
	if target == "terminal" {
		script = "sudo -H -u cage /usr/local/bin/cage-desktop-files --save-file " + shellescape.Quote(name)
	} else {
		if body != nil {
			script += " --upload-file -"
		}
		script += " " + shellescape.Quote("http://localhost"+path)
	}
	cmd := s.sshCommand(ctx, "-T", s.sshTarget(), "--", script)
	cmd.Stdin = body
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, errors.New("无法连接文件传递服务")
	}
	data, readErr := io.ReadAll(io.LimitReader(pipe, 65537))
	if readErr != nil || len(data) > 65536 {
		_ = cmd.Process.Kill()
	}
	err = cmd.Wait()
	var result map[string]any
	if err != nil || readErr != nil || len(data) > 65536 || json.Unmarshal(data, &result) != nil {
		return nil, errors.New("文件传递未完成，请确认应用已打开；环境组件过旧时请先更新环境")
	}
	return result, nil
}

func (s *Server) fileTargetReady(target, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return false
	}
	if target == "terminal" {
		terminal := s.terminals.get(id)
		if terminal == nil {
			return false
		}
		terminal.mu.Lock()
		defer terminal.mu.Unlock()
		return !terminal.closed
	}
	return target == "browser" || target == "desktop"
}

func (s *Server) fileInputAction(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Target    string  `json:"target"`
		ID        string  `json:"id"`
		Kind      string  `json:"kind"`
		X         float64 `json:"x"`
		Y         float64 `json:"y"`
		Workspace string  `json:"workspace"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	action := r.PathValue("action")
	if action != "start" && action != "finish" && action != "cancel" && action != "directory" && action != "download-retry" {
		http.NotFound(w, r)
		return
	}
	if !(action == "start" && (input.Target == "browser" && input.ID != "" && len(input.ID) <= 256 || input.Target == "desktop")) && !validFileTargetID(input.Target, input.ID) {
		http.Error(w, "文件目标无效", 400)
		return
	}
	if !s.fileTargetReady(input.Target, input.ID) {
		http.Error(w, "文件目标已关闭或环境尚未就绪", 409)
		return
	}
	if action == "start" && input.Kind != "drop" && input.Kind != "paste" {
		http.Error(w, "文件传递方式无效", 400)
		return
	}
	if (action == "directory" || action == "download-retry") && input.Target != "desktop" {
		http.Error(w, "文件夹目标无效", 400)
		return
	}
	if action == "directory" {
		if !s.op.TryLock() {
			http.Error(w, "另一个操作仍在进行，请稍候", 409)
			return
		}
		defer s.op.Unlock()
		if !s.fileTargetReady("desktop", input.ID) || s.busy() {
			http.Error(w, "环境尚未就绪或正在处理其他操作，请稍后重试", 409)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	body, _ := json.Marshal(input)
	if action == "directory" {
		if input.Workspace == "" {
			http.Error(w, "请选择已打开的项目", 400)
			return
		}
		workspace, err := s.connectProject(input.Workspace, false)
		if err != nil {
			requestError(w, err)
			return
		}
		body, _ = json.Marshal(map[string]string{"id": input.ID, "path": guestPath(workspace)})
	}
	if input.Target == "terminal" {
		jsonReply(w, map[string]any{"ok": true, "id": input.ID})
		return
	}
	if input.Target == "browser" {
		s.mu.Lock()
		token := s.credentials.Token
		s.mu.Unlock()
		data, err := s.guestAPI(ctx, token, "POST", "/upload-"+action, body)
		if err != nil {
			requestError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
		return
	}
	result, err := s.guestFiles(ctx, "desktop", "POST", "/"+action, bytes.NewReader(body), "")
	if err != nil {
		requestError(w, err)
		return
	}
	jsonReply(w, result)
}

func (s *Server) desktopFileRequests(w http.ResponseWriter, r *http.Request) {
	if !s.fileTargetReady("desktop", "") {
		http.Error(w, "环境尚未就绪", 409)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	result, err := s.guestFiles(ctx, "desktop", "GET", "/requests", nil, "")
	if err != nil {
		requestError(w, err)
		return
	}
	if err = s.rememberDesktopDownloads(result); err != nil {
		requestError(w, err)
		return
	}
	jsonReply(w, result)
}

// Register completed exports before the UI can ask a native bridge to save them.
// Guest paths are never accepted as arbitrary host paths.
func (s *Server) rememberDesktopDownloads(result map[string]any) error {
	items, _ := result["downloads"].([]any)
	for _, value := range items {
		item, ok := value.(map[string]any)
		if !ok {
			return errors.New("Desktop 下载记录无效")
		}
		id, _ := item["id"].(string)
		name, _ := item["name"].(string)
		if !browserFileID.MatchString(id) || !browserFileName(name) {
			return errors.New("Desktop 下载记录无效")
		}
		item["target"] = "desktop"
		if item["state"] != "saved" {
			delete(item, "path")
			continue
		}
		root := s.environmentDir()
		if item["path"] != strings.TrimRight(guestPath(root), "/")+"/Downloads/"+name {
			return errors.New("Desktop 下载保存位置无效")
		}
		path := filepath.Join(root, "Downloads", name)
		if err := rememberDownload(s.profile, SavedDownload{ID: id, Name: name, Path: path}, true); err != nil {
			return err
		}
		item["path"] = path
	}
	return nil
}

func (s *Server) fileInputUpload(w http.ResponseWriter, r *http.Request) {
	target, id, name := r.PathValue("target"), r.PathValue("id"), r.URL.Query().Get("name")
	if !validFileTargetID(target, id) || !browserFileName(name) {
		http.Error(w, "文件名或上传目标无效", 400)
		return
	}
	if !s.fileTargetReady(target, id) {
		http.Error(w, "文件目标已关闭或环境尚未就绪", 409)
		return
	}
	if target == "browser" {
		s.browserUpload(w, r)
		return
	}
	if r.ContentLength > browserFileLimit {
		http.Error(w, "每个文件不能超过 2 GB", 413)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	r.Body = http.MaxBytesReader(w, r.Body, browserFileLimit)
	result, err := s.guestFiles(ctx, target, "POST", "/upload?"+url.Values{"id": {id}, "name": {name}}.Encode(), r.Body, name)
	if err != nil {
		requestError(w, err)
		return
	}
	if target == "terminal" {
		path, _ := result["path"].(string)
		if !strings.HasPrefix(path, "/home/cage/.local/share/cage/attachments/files/") || strings.ContainsAny(path, "\x00\r\n") {
			http.Error(w, "文件保存路径无效", 502)
			return
		}
	}
	jsonReply(w, result)
}
