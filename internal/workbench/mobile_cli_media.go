package workbench

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/sylvinus/agent-vm/internal/paths"
)

const mobileMediaLimit = 48 << 20
const mobileMediaChunk = 512 << 10

type mediaChunkRequest struct {
	Reference string `json:"reference"`
	Offset    int64  `json:"offset"`
	Version   string `json:"version"`
}

func mediaFileName(workspace, cwd, reference string) (string, error) {
	value, err := url.PathUnescape(reference)
	if err != nil || len(value) > 4096 || value == "" || strings.ContainsRune(value, 0) || strings.HasPrefix(value, "//") {
		return "", fs.ErrInvalid
	}
	root := guestPath(workspace)
	if filepath.IsAbs(value) {
		value = guestPath(value)
	}
	if cwd == "" {
		cwd = root
	}
	if !path.IsAbs(value) {
		value = path.Join(cwd, value)
	}
	name, ok := paths.CutPrefix(path.Clean(value), strings.TrimSuffix(root, "/")+"/")
	if !ok || !fs.ValidPath(name) || strings.ContainsAny(name, ":\\") {
		return "", fs.ErrPermission
	}
	return filepath.Localize(name)
}

// Require both the filename and the file signature to identify a passive media
// format. No HTML, SVG, arbitrary file downloads or URL fetches use this route.
func mediaFormat(name string, head []byte) string {
	kind := http.DetectContentType(head)
	ext := strings.ToLower(filepath.Ext(name))
	expected := map[string]string{
		".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png",
		".gif": "image/gif", ".webp": "image/webp", ".webm": "video/webm",
		".mp4": "video/mp4", ".m4v": "video/mp4",
	}
	if kind == expected[ext] {
		return kind
	}
	if ext == ".mov" && len(head) >= 12 && bytes.Equal(head[4:12], []byte("ftypqt  ")) {
		return "video/quicktime"
	}
	return ""
}

func (s *Server) mobileSessionMedia(w http.ResponseWriter, r *http.Request) {
	var in mediaChunkRequest
	if !readJSON(w, r, &in) {
		return
	}
	view, _, ok := s.authorizedMobileSessionRecord(w, r)
	if !ok {
		return
	}
	if view.Archived || isClientMountPath(view.Workspace) {
		http.Error(w, "此项目当前无法预览文件。", 409)
		return
	}
	if in.Reference == "" || len(in.Reference) > 8192 || in.Offset < 0 || in.Offset >= mobileMediaLimit || (in.Offset != 0 && in.Version == "") {
		http.Error(w, "预览请求无效。", 400)
		return
	}
	// Use the transcript snapshot already sent to this device. Asset chunks
	// must not trigger a new guest SSH scan of the entire conversation.
	found := false
	for _, turn := range view.Turns {
		if strings.Contains(turn.Text, in.Reference) || strings.Contains(turn.Prompt, in.Reference) {
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "此文件没有出现在会话中。", 403)
		return
	}
	var name string
	var err error
	if s.windowsGuest != nil {
		project, lookup := s.windowsProjectForHost(view.Workspace)
		if lookup != nil {
			http.Error(w, "Windows 项目连接已撤销，请重新连接项目。", http.StatusConflict)
			return
		}
		name, err = windowsMediaFileName(project.Path, view.mediaWorkingDir, in.Reference)
	} else {
		name, err = mediaFileName(s.cliWorkspace(view.Workspace), view.mediaWorkingDir, in.Reference)
	}
	if err != nil {
		http.Error(w, "只能预览当前项目中的媒体文件。", 403)
		return
	}
	canonical, err := paths.Real(view.Workspace)
	if err != nil || !paths.Equal(canonical, paths.Host(view.Workspace)) {
		http.Error(w, "项目目录已变化，请重新打开项目。", 409)
		return
	}
	root, err := os.OpenRoot(canonical)
	if err != nil {
		http.Error(w, "项目目录暂时不可用。", 404)
		return
	}
	defer root.Close()
	info, err := root.Stat(name)
	if err != nil || !info.Mode().IsRegular() {
		http.Error(w, "文件已移动或不可读取。", 404)
		return
	}
	file, err := root.Open(name)
	if err != nil {
		http.Error(w, "文件已移动或不可读取。", 404)
		return
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > mobileMediaLimit {
		http.Error(w, "手机支持预览 48 MiB 以内的非空媒体文件。", 413)
		return
	}
	version := fmt.Sprintf("%x-%x", info.Size(), info.ModTime().UnixNano())
	if in.Offset >= info.Size() || (in.Version != "" && in.Version != version) {
		http.Error(w, "文件已变化，请重新加载预览。", 409)
		return
	}
	var head [512]byte
	n, err := file.ReadAt(head[:], 0)
	if err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "读取文件失败。", 409)
		return
	}
	kind := mediaFormat(name, head[:n])
	if kind == "" {
		http.Error(w, "此文件的格式无法在手机预览。", 415)
		return
	}
	chunk := make([]byte, min(int64(mobileMediaChunk), info.Size()-in.Offset))
	if _, err := file.ReadAt(chunk, in.Offset); err != nil {
		http.Error(w, "文件已变化，请重新加载预览。", 409)
		return
	}
	end, err := file.Stat()
	if err != nil || end.Size() != info.Size() || !end.ModTime().Equal(info.ModTime()) {
		http.Error(w, "文件已变化，请重新加载预览。", 409)
		return
	}
	jsonReply(w, map[string]any{"mime": kind, "size": info.Size(), "version": version, "data": chunk})
}
