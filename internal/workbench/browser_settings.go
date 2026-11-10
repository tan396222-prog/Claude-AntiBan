package workbench

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

func (s *Server) browserPreferences(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.mu.Lock()
		home := browserHomeURL(s.preferences)
		s.mu.Unlock()
		jsonReply(w, map[string]string{"browser_home_url": home})
		return
	}
	var input struct {
		Home string `json:"browser_home_url"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	s.saveBrowserHome(w, r, input.Home)
}

func validateBrowserHome(value string) (string, error) {
	home := strings.TrimSpace(value)
	parsed, err := url.Parse(home)
	if err != nil || len(home) > 8192 || parsed == nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Hostname() == "" || parsed.User != nil {
		return "", errors.New("新标签页地址必须是完整的 HTTP 或 HTTPS 网址，且不能包含用户名或密码")
	}
	return home, nil
}

func (s *Server) saveBrowserHome(w http.ResponseWriter, r *http.Request, value string) {
	home, err := validateBrowserHome(value)
	if err != nil {
		requestError(w, err)
		return
	}
	if !s.op.TryLock() {
		http.Error(w, "环境正在执行其他操作，请稍后保存", http.StatusConflict)
		return
	}
	defer s.op.Unlock()
	s.mu.Lock()
	previous, ready, token, closing := s.preferences, s.ready, s.credentials.Token, s.closing
	s.mu.Unlock()
	if closing {
		http.Error(w, "服务正在关闭", http.StatusServiceUnavailable)
		return
	}
	if ready {
		if err = s.setBrowserHome(r.Context(), token, home); err != nil {
			requestError(w, err)
			return
		}
	}
	next := previous
	next.BrowserHomeURL = home
	if err = writePrivate(filepath.Join(s.profile, "workbench.json"), next); err != nil {
		if ready {
			_ = s.setBrowserHome(r.Context(), token, browserHomeURL(previous))
		}
		requestError(w, errors.New("保存浏览器设置失败，请重试"))
		return
	}
	s.mu.Lock()
	s.preferences = next
	s.mu.Unlock()
	jsonReply(w, map[string]string{"browser_home_url": home})
}

func (s *Server) browserExtensionUpload(w http.ResponseWriter, r *http.Request) {
	const limit = 32 << 20
	if r.ContentLength > limit {
		http.Error(w, "扩展 ZIP 不能超过 32 MB", 413)
		return
	}
	s.mu.Lock()
	ready, token := s.ready, s.credentials.Token
	s.mu.Unlock()
	if !ready {
		http.Error(w, "环境尚未就绪", 409)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if s.macosGuest != nil || s.windowsGuest != nil {
		data, err := s.nativeBrowserClient().uploadExtension(r.Context(), r.Body, r.ContentLength)
		if err != nil {
			requestError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
		return
	}
	response, err := s.browserClient().file(r.Context(), token, http.MethodPost, "/extensions-upload", r.Body, r.ContentLength)
	if err != nil {
		http.Error(w, "扩展上传中断，请重试", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, browserResponseLimit+1))
	if err != nil || len(data) > browserResponseLimit {
		http.Error(w, "扩展包响应无效，请重试", http.StatusBadGateway)
		return
	}
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &failure)
		if failure.Error == "" {
			failure.Error = "扩展包读取失败，请选择有效的扩展 ZIP"
		}
		http.Error(w, failure.Error, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}
