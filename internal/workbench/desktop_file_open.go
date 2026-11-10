package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sylvinus/agent-vm/internal/host"
)

type desktopFileOpen struct {
	ID      string `json:"id,omitempty"`
	Path    string `json:"path,omitempty"`
	Action  string `json:"action,omitempty"`
	Handled bool   `json:"handled,omitempty"`
}

// Resolve a guest path against directory grants, never against the host root.
// Both lexical traversal and a host-side symlink out of the project are denied.
func resolveProjectFilePath(guest string, projects []string) (string, error) {
	if !pathpkg.IsAbs(guest) || pathpkg.Clean(guest) != guest || strings.ContainsAny(guest, "\x00\\") {
		return "", errors.New("文件位置无效")
	}
	for _, project := range projects {
		if isClientMountPath(project) {
			continue
		}
		root := strings.TrimRight(guestPath(project), "/")
		if guest != root && !strings.HasPrefix(guest, root+"/") {
			continue
		}
		hostRoot, err := filepath.EvalSymlinks(project)
		if err != nil {
			continue
		}
		target, err := filepath.EvalSymlinks(filepath.Join(hostRoot, filepath.FromSlash(strings.TrimPrefix(guest, root))))
		if err != nil {
			return "", errors.New("文件已移动、删除或无法访问")
		}
		relative, err := filepath.Rel(hostRoot, target)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return "", errors.New("文件指向 Cage 项目目录之外，无法打开")
		}
		info, err := os.Stat(target)
		if err != nil || (!info.Mode().IsRegular() && !info.IsDir()) {
			return "", errors.New("请选择 Cage 项目中的普通文件或文件夹")
		}
		return target, nil
	}
	return "", errors.New("只能打开 Cage 项目目录中的文件和文件夹")
}

func (s *Server) resolveDesktopProjectFile(guest string, projects []string) (string, error) {
	if s.macosGuest == nil && s.windowsGuest == nil {
		return resolveProjectFilePath(guest, projects)
	}
	if s.windowsGuest != nil {
		guest = strings.ReplaceAll(guest, "\\", "/")
		if !windowsCLIAbsolutePath(guest) || strings.Contains(guest[2:], ":") {
			return "", errors.New("文件位置无效")
		}
	}
	if pathpkg.Clean(guest) != guest || strings.ContainsAny(guest, "\x00\\") {
		return "", errors.New("文件位置无效")
	}
	for _, project := range projects {
		if isClientMountPath(project) {
			continue
		}
		var root string
		if s.macosGuest != nil {
			root = macOSProjectPath(s.macosGuest.User, project)
		} else {
			binding, err := s.windowsProjectForHost(project)
			if err != nil {
				continue
			}
			root = strings.ReplaceAll(binding.Path, "\\", "/")
		}
		root = strings.TrimRight(root, "/")
		if !cliWorkspaceWithin(guest, root, s.windowsGuest != nil) {
			continue
		}
		mapped := strings.TrimRight(guestPath(project), "/") + guest[len(root):]
		return resolveProjectFilePath(mapped, []string{project})
	}
	return "", errors.New("只能打开已授权 Cage 项目中的文件和文件夹")
}

func (s *Server) desktopFileOpenAction(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID    string `json:"id"`
		Stage string `json:"stage"`
		Error string `json:"error"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if !browserFileID.MatchString(input.ID) || (input.Stage != "claim" && input.Stage != "complete") {
		http.Error(w, "文件打开请求无效", 400)
		return
	}
	if !s.fileTargetReady("desktop", "") {
		http.Error(w, "环境尚未就绪", 409)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	body, _ := json.Marshal(input)
	result, err := s.guestFiles(ctx, "desktop", "POST", "/open-"+input.Stage, bytes.NewReader(body), "")
	if err != nil {
		requestError(w, err)
		return
	}
	if input.Stage == "complete" || result["handled"] == true {
		jsonReply(w, result)
		return
	}
	guest, _ := result["path"].(string)
	action, _ := result["action"].(string)
	var target string
	download, _ := result["download_id"].(string)
	if message, _ := result["error"].(string); message != "" {
		err = errors.New(message)
	} else if action != "open" && action != "reveal" {
		err = errors.New("文件打开操作无效")
	} else if download != "" && (s.macosGuest != nil || s.windowsGuest != nil) {
		var saved browserDownloadInfo
		saved, err = s.downloadInfo(ctx, download)
		if err == nil && saved.State != "saved" {
			err = errors.New("文件尚未完整保存到主机，请稍后重新打开")
		}
		target = saved.Path
		if err == nil && result["directory"] == true {
			target = filepath.Dir(target)
		}
	} else {
		var projects []string
		projects, err = s.projectPaths()
		if err == nil {
			target, err = s.resolveDesktopProjectFile(guest, projects)
		}
	}
	if err != nil {
		body, _ = json.Marshal(map[string]string{"id": input.ID, "error": err.Error()})
		cleanup, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancelCleanup()
		_, _ = s.guestFiles(cleanup, "desktop", "POST", "/open-complete", bytes.NewReader(body), "")
		requestError(w, err)
		return
	}
	jsonReply(w, desktopFileOpen{ID: input.ID, Path: target, Action: action})
}

// DesktopFileForConnection consumes one pending app request. The web bridge
// supplies only an opaque ID; the authenticated local controller owns its path.
func DesktopFileForConnection(configPath, connectionID, id string) (map[string]any, error) {
	if !browserFileID.MatchString(id) {
		return nil, errors.New("文件打开标识无效")
	}
	client, err := connectController(filepath.Dir(configPath))
	if err != nil {
		return nil, err
	}
	defer client.close()
	entry, _ := url.Parse(client.launch.URL)
	status, err := client.call("POST", "/api/session", map[string]string{"token": entry.Fragment}, nil)
	if err != nil || status != 200 {
		return nil, errors.New("无法连接文件操作会话")
	}
	connection, err := client.bindConnection(connectionID)
	if err != nil {
		return nil, err
	}
	var request desktopFileOpen
	err = desktopOpenCall(client, map[string]string{"id": id, "stage": "claim"}, &request)
	if err != nil {
		return nil, err
	}
	if request.Handled {
		return map[string]any{"handled": true}, nil
	}
	if connection.Mode == "remote" {
		err = errors.New("此文件位于远程环境，请先在下载列表中保存到本机")
	} else if request.ID != id || !filepath.IsAbs(request.Path) || (request.Action != "open" && request.Action != "reveal") {
		err = errors.New("文件打开响应无效")
	} else {
		err = openHostFile(request.Path, request.Action)
	}
	message := ""
	if err != nil {
		message = err.Error()
	}
	completeErr := desktopOpenCall(client, map[string]string{"id": id, "stage": "complete", "error": message}, nil)
	if err != nil {
		return nil, err
	}
	if completeErr != nil {
		return nil, completeErr
	}
	return map[string]any{"ok": true}, nil
}

func desktopOpenCall(client *controllerClient, input map[string]string, output any) error {
	body, _ := json.Marshal(input)
	request, _ := http.NewRequest("POST", client.origin+"/api/input/files/desktop/open", bytes.NewReader(body))
	request.Header.Set("Origin", client.origin)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.client.Do(request)
	if err != nil {
		return errors.New("文件操作连接已中断，请重新打开")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(data) > 65536 {
		return errors.New("文件操作响应无效")
	}
	if response.StatusCode != http.StatusOK {
		message := strings.TrimSpace(string(data))
		if message == "" {
			message = "文件打开请求已失效，请重新操作"
		}
		return errors.New(message)
	}
	if output != nil && json.Unmarshal(data, output) != nil {
		return errors.New("文件操作响应无效")
	}
	return nil
}

func hostFileCommand(platform, path, action string, directory bool) *exec.Cmd {
	switch platform {
	case "darwin":
		if action == "reveal" {
			return exec.Command("/usr/bin/open", "-R", "--", path)
		}
		return exec.Command("/usr/bin/open", "--", path)
	case "windows":
		if action == "reveal" {
			return exec.Command("explorer.exe", "/select,"+path)
		}
		if directory {
			return exec.Command("explorer.exe", path)
		}
		return exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", path)
	default:
		if action == "reveal" {
			uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
			return exec.Command("gdbus", "call", "--session", "--dest", "org.freedesktop.FileManager1", "--object-path", "/org/freedesktop/FileManager1", "--method", "org.freedesktop.FileManager1.ShowItems", "[\""+uri+"\"]", "")
		}
		return exec.Command("gio", "open", path)
	}
}

func openHostFile(path, action string) error {
	info, err := os.Stat(path)
	if err != nil || (!info.Mode().IsRegular() && !info.IsDir()) {
		return errors.New("文件已移动、删除或无法访问")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := hostFileCommand(runtime.GOOS, path, action, info.IsDir())
	cmd := exec.CommandContext(ctx, command.Path, command.Args[1:]...)
	host.HideCommandWindow(cmd)
	if err = cmd.Run(); err != nil {
		if runtime.GOOS != "linux" || action != "reveal" {
			return errors.New("无法打开本机文件或文件管理器，请检查默认应用和文件权限")
		}
		fallback := exec.CommandContext(ctx, "gio", "open", filepath.Dir(path))
		if fallback.Run() != nil {
			return errors.New("无法打开本机文件管理器，请检查桌面环境和文件权限")
		}
	}
	return nil
}
