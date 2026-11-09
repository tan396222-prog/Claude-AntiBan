package workbench

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/paths"
)

// Directory browsing lists names without granting a share. The existing
// projects/open operation remains the only authority to add a project.
func (s *Server) projectDirectories(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path, _ = os.UserHomeDir()
	}
	if !filepath.IsAbs(path) {
		http.Error(w, "请选择服务器上的绝对路径", 400)
		return
	}
	real, err := paths.Resolve(path)
	if err != nil {
		http.Error(w, "服务器目录不存在或无法访问", 400)
		return
	}
	reserved, err := privateProjectDirectories(s.profile)
	if err != nil {
		http.Error(w, "无法验证环境存储目录", 500)
		return
	}
	private := func(path string) bool {
		for _, dir := range reserved {
			if cage.StorageContains(dir, path) {
				return true
			}
		}
		return false
	}
	if private(real) {
		http.Error(w, "Cage 配置目录不能作为项目浏览", 403)
		return
	}
	directory, err := os.Open(real)
	if err != nil {
		http.Error(w, "无法读取服务器目录", 403)
		return
	}
	defer directory.Close()
	entries, err := directory.ReadDir(2001)
	if err != nil && err != io.EOF {
		http.Error(w, "无法读取服务器目录", 403)
		return
	}
	if len(entries) > 2000 {
		http.Error(w, "此目录项目过多，请直接输入更具体的路径", 413)
		return
	}
	type child struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	children := []child{}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(real, entry.Name())
		if physical, err := paths.Resolve(path); err == nil && !private(physical) {
			children = append(children, child{Name: entry.Name(), Path: physical})
		}
	}
	sort.Slice(children, func(i, j int) bool { return children[i].Name < children[j].Name })
	_, selectableError := validateWorkspace(real, s.profile)
	jsonReply(w, map[string]any{"path": real, "parent": filepath.Dir(real), "directories": children, "selectable": selectableError == nil})
}
