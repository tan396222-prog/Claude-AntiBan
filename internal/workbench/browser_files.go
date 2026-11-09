package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const browserFileLimit int64 = 2 << 30

var browserFileID = regexp.MustCompile(`^[a-fA-F0-9]{8}(?:-[a-fA-F0-9]{4}){3}-[a-fA-F0-9]{12}$`)

func browserFileName(name string) bool {
	return len(name) <= 255 && browserDownloadName(name)
}
func browserDownloadName(name string) bool {
	return name != "" && len(name) <= 1024 && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\x00\r\n")
}

func (s *Server) browserUpload(w http.ResponseWriter, r *http.Request) {
	id, name := r.PathValue("id"), r.URL.Query().Get("name")
	if !browserFileID.MatchString(id) || !browserFileName(name) {
		http.Error(w, "文件名或上传窗口无效", 400)
		return
	}
	if r.ContentLength > browserFileLimit {
		http.Error(w, "每个文件不能超过 2 GB", 413)
		return
	}
	s.mu.Lock()
	ready, token := s.ready, s.credentials.Token
	s.mu.Unlock()
	if !ready {
		http.Error(w, "环境尚未就绪", 409)
		return
	}
	// Never accept a file without a live chooser owned by the remote browser.
	snapshot, err := s.guestAPI(r.Context(), token, "GET", "/tabs", nil)
	var state struct {
		Uploads []struct {
			ID string `json:"id"`
		} `json:"uploads"`
	}
	if err != nil || json.Unmarshal(snapshot, &state) != nil {
		http.Error(w, "无法连接上传窗口", 502)
		return
	}
	found := false
	for _, upload := range state.Uploads {
		if upload.ID == id {
			found = true
		}
	}
	if !found {
		http.Error(w, "上传窗口已关闭，请重新点击网页上传按钮", 409)
		return
	}
	query := url.Values{"id": {id}, "name": {name}}
	r.Body = http.MaxBytesReader(w, r.Body, browserFileLimit)
	s.proxyBrowserFile(w, r, token, "POST", "/upload-file?"+query.Encode(), r.Body)
}

func (s *Server) proxyBrowserFile(w http.ResponseWriter, r *http.Request, token, method, path string, body io.Reader) {
	response, err := s.browserClient().file(r.Context(), token, method, path, body, r.ContentLength)
	if err != nil {
		http.Error(w, "文件传递中断，请重试", 502)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		http.Error(w, "文件不可用或上传窗口已关闭，请重试", 502)
		return
	}
	if method == "POST" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.Copy(w, io.LimitReader(response.Body, 4096))
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	if length := response.Header.Get("Content-Length"); length != "" {
		w.Header().Set("Content-Length", length)
	}
	_, _ = io.Copy(w, response.Body)
}

type browserDownloadInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	State     string `json:"state"`
	Path      string `json:"path,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
}

func (s *Server) downloadInfo(ctx context.Context, id string) (browserDownloadInfo, error) {
	if !browserFileID.MatchString(id) {
		return browserDownloadInfo{}, errors.New("下载标识无效")
	}
	if saved, err := readSavedDownload(s.profile, id); err == nil {
		return browserDownloadInfo{ID: id, Name: saved.Name, Path: saved.Path, State: "saved"}, nil
	}
	s.mu.Lock()
	ready, token := s.ready, s.credentials.Token
	s.mu.Unlock()
	if !ready {
		return browserDownloadInfo{}, errors.New("请先启动环境或从已保存的位置打开文件")
	}
	data, err := s.guestAPI(ctx, token, "GET", "/tabs", nil)
	if err != nil {
		return browserDownloadInfo{}, err
	}
	var state struct {
		Downloads []browserDownloadInfo `json:"downloads"`
	}
	if json.Unmarshal(data, &state) != nil {
		return browserDownloadInfo{}, errors.New("下载列表无效")
	}
	for _, item := range state.Downloads {
		if item.ID != id || !browserDownloadName(item.Name) {
			continue
		}
		if item.State == "saved" {
			root := s.environmentDir()
			expected := strings.TrimRight(guestPath(root), "/") + "/Downloads/" + item.Name
			if item.Path != expected {
				return browserDownloadInfo{}, errors.New("下载保存位置无效")
			}
			item.Path = filepath.Join(root, "Downloads", item.Name)
			if err = rememberDownload(s.profile, SavedDownload{ID: id, Name: item.Name, Path: item.Path}, true); err != nil {
				return browserDownloadInfo{}, err
			}
		} else {
			item.Path = ""
		}
		return item, nil
	}
	return browserDownloadInfo{}, errors.New("下载记录已不存在，请从原网页重新下载")
}

func (s *Server) browserDownload(w http.ResponseWriter, r *http.Request) {
	if action := r.PathValue("action"); action != "" && action != "content" {
		http.NotFound(w, r)
		return
	}
	item, err := s.downloadInfo(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	if r.PathValue("action") != "content" {
		jsonReply(w, item)
		return
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": item.Name}))
	if item.Path != "" {
		file, err := openDownloadedFile(item.Path)
		if err != nil {
			http.Error(w, "文件已移动、删除或暂时无法读取", 404)
			return
		}
		defer file.Close()
		info, _ := file.Stat()
		http.ServeContent(w, r, item.Name, info.ModTime(), cancelableDownload{ReadSeeker: file, ctx: r.Context()})
		return
	}
	if item.State != "failed" || !item.Retryable {
		http.Error(w, "文件尚未下载完成或已不可用", 409)
		return
	}
	s.mu.Lock()
	token := s.credentials.Token
	s.mu.Unlock()
	s.proxyBrowserFile(w, r, token, "GET", "/download-file?id="+url.QueryEscape(item.ID), nil)
}

type cancelableDownload struct {
	io.ReadSeeker
	ctx context.Context
}

func (reader cancelableDownload) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.ReadSeeker.Read(buffer)
}

// A file replaced by a symlink must not turn a download action into an arbitrary
// host read. The selected file is checked again after opening it.
func openDownloadedFile(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() {
		return nil, errors.New("下载文件不可用")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	if filepath.Clean(parent) != filepath.Clean(filepath.Dir(path)) {
		return nil, errors.New("下载目录已被替换")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) {
		file.Close()
		return nil, errors.New("下载文件已变化")
	}
	return file, nil
}
