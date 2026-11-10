package workbench

import (
	"errors"
	"io/fs"
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

// The host owns the record and live mount. No phone parameter supplies a guest
// directory, project grant, or execution identity to the native CLI.
func (s *Server) windowsConversationInput(record *mobileRecord, input map[string]any) error {
	if s.windowsGuest == nil {
		return nil
	}
	if _, err := s.projectWorkspace(record.Workspace); err != nil {
		return err
	}
	project, err := s.windowsProjectForHost(record.Workspace)
	if err != nil {
		return err
	}
	input["workspace"], input["project_id"], input["grant"] = project.Path, project.ID, project.Grant
	return nil
}

var errWindowsLegacyMobile = errors.New("这段历史来自尚未适配 Windows 的旧移动会话，记录已保留；请在原运行环境接续，或在 Windows 新建共享会话")

func windowsCLIAbsolutePath(value string) bool {
	return len(value) > 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && (value[2] == '\\' || value[2] == '/')
}

func windowsMediaFileName(root, cwd, reference string) (string, error) {
	value, err := url.PathUnescape(reference)
	if err != nil || value == "" || len(value) > 4096 || strings.ContainsRune(value, 0) {
		return "", fs.ErrInvalid
	}
	normalize := func(value string) string { return strings.ReplaceAll(value, "\\", "/") }
	root, cwd, value = strings.TrimRight(normalize(root), "/"), normalize(cwd), normalize(value)
	if !windowsCLIAbsolutePath(root) || strings.HasPrefix(value, "/") {
		return "", fs.ErrPermission
	}
	if cwd == "" {
		cwd = root
	}
	if !cliWorkspaceWithin(path.Clean(cwd), root, true) {
		return "", fs.ErrPermission
	}
	if !windowsCLIAbsolutePath(value) {
		if strings.Contains(value, ":") {
			return "", fs.ErrPermission
		}
		value = path.Join(cwd, value)
	}
	value = path.Clean(value)
	if !cliWorkspaceWithin(value, root, true) || len(value) <= len(root)+1 {
		return "", fs.ErrPermission
	}
	name := value[len(root)+1:]
	if !fs.ValidPath(name) || strings.Contains(name, ":") {
		return "", fs.ErrPermission
	}
	return filepath.Localize(name)
}
