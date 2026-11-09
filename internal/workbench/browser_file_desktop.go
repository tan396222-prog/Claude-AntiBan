package workbench

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/sylvinus/agent-vm/internal/host"
)

type SavedDownload struct {
	InstanceID string    `json:"instance_id,omitempty"`
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	SavedAt    time.Time `json:"saved_at"`
}

func savedDownloadPath(profile, id string) string {
	return filepath.Join(profile, "browser-downloads", id+".json")
}
func readSavedDownload(profile, id string) (SavedDownload, error) {
	var saved SavedDownload
	if !browserFileID.MatchString(id) {
		return saved, errors.New("下载标识无效")
	}
	path := savedDownloadPath(profile, id)
	if err := host.CheckPrivateFile(path); err != nil {
		return saved, err
	}
	f, err := os.Open(path)
	if err != nil {
		return saved, err
	}
	defer f.Close()
	if err = json.NewDecoder(io.LimitReader(f, 16384)).Decode(&saved); err != nil {
		return saved, err
	}
	if saved.ID != id || !filepath.IsAbs(saved.Path) || !browserDownloadName(saved.Name) {
		return saved, errors.New("下载位置记录无效")
	}
	return saved, nil
}
func rememberDownload(profile string, saved SavedDownload, firstOnly bool) error {
	if !browserFileID.MatchString(saved.ID) {
		return errors.New("下载标识无效")
	}
	directory := filepath.Join(profile, "browser-downloads")
	if err := host.EnsurePrivateDir(directory); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(directory, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	for attempts := 0; ; attempts++ {
		if err = lockController(lock); err == nil {
			break
		}
		if attempts >= 40 {
			return errors.New("下载位置正在更新，请重试")
		}
		time.Sleep(25 * time.Millisecond)
	}
	defer unlockController(lock)
	if firstOnly {
		if _, err = readSavedDownload(profile, saved.ID); err == nil {
			return nil
		}
	}
	saved.SavedAt = time.Now().UTC()
	return writePrivate(savedDownloadPath(profile, saved.ID), saved)
}
func (s *Server) savedBrowserDownloads(w http.ResponseWriter, r *http.Request) {
	serveSavedDownloads(w, s.profile)
}

func serveSavedDownloads(w http.ResponseWriter, profile string) {
	entries, err := os.ReadDir(filepath.Join(profile, "browser-downloads"))
	if err != nil && !os.IsNotExist(err) {
		http.Error(w, "无法读取已保存的位置", 500)
		return
	}
	records := []SavedDownload{}
	for _, entry := range entries {
		name := entry.Name()
		if len(name) != 41 {
			continue
		}
		if saved, err := readSavedDownload(profile, name[:36]); err == nil {
			records = append(records, saved)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].SavedAt.After(records[j].SavedAt) })
	if len(records) > 100 {
		records = records[:100]
	}
	jsonReply(w, records)
}

// BrowserFile is called by native dialogs, which supply the chosen path. HTTP
// clients can stream known downloads but cannot supply an arbitrary host path.
func BrowserFile(configPath, action, id, destination string) (SavedDownload, error) {
	return BrowserFileForConnection(configPath, "", action, id, destination)
}

func BrowserFileForConnection(configPath, connectionID, action, id, destination string) (SavedDownload, error) {
	var result SavedDownload
	if !browserFileID.MatchString(id) || (action != "save" && action != "open" && action != "reveal") {
		return result, errors.New("下载操作无效")
	}
	client, err := connectController(filepath.Dir(configPath))
	if err != nil {
		return result, err
	}
	defer client.close()
	entry, _ := url.Parse(client.launch.URL)
	status, err := client.call("POST", "/api/session", map[string]string{"token": entry.Fragment}, nil)
	if err != nil || status != 200 {
		return result, errors.New("无法连接下载会话")
	}
	connection, err := client.bindConnection(connectionID)
	if err != nil {
		return result, err
	}
	profile := filepath.Dir(configPath)
	if connection.DesktopGateway && connection.Mode == "local" {
		profile = filepath.Dir(profile)
	}
	saveProfile, err := clientDownloadProfile(profile, connection)
	if err != nil {
		return result, err
	}
	var item browserDownloadInfo
	if connection.Mode == "remote" && action != "save" {
		saved, readErr := readSavedDownload(saveProfile, id)
		if readErr != nil || saved.InstanceID != connection.InstanceID {
			return result, errors.New("请先将文件另存为到本机，再打开或显示所在位置")
		}
		result = saved
	} else {
		status, err = client.call("GET", "/api/browser/files/downloads/"+id, nil, &item)
		if err != nil || status != 200 {
			return result, errors.New("下载文件不可用，请确认 Cage 已解锁且下载已完成")
		}
		result = SavedDownload{ID: id, Name: item.Name, Path: item.Path}
		if connection.Mode == "remote" {
			result.Path = ""
			result.InstanceID = connection.InstanceID
		}
	}
	if action == "save" {
		if !filepath.IsAbs(destination) || filepath.Base(destination) == "." {
			return result, errors.New("保存位置无效")
		}
		parent, resolveErr := filepath.EvalSymlinks(filepath.Dir(destination))
		if resolveErr != nil {
			return result, errors.New("所选保存文件夹已不可用")
		}
		destination = filepath.Join(parent, filepath.Base(destination))
		request, _ := http.NewRequest("GET", client.origin+"/api/browser/files/downloads/"+id+"/content", nil)
		request.Header.Set("Origin", client.origin)
		client.client.Timeout = 30 * time.Minute
		response, err := client.client.Do(request)
		if err != nil {
			return result, errors.New("无法读取下载文件，请重试")
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return result, errors.New("下载源文件已不可用，请从原网页重新下载")
		}
		if err = saveDownloadedStream(destination, response.Body, response.ContentLength); err != nil {
			return result, err
		}
		result.Path = destination
		result.Name = filepath.Base(destination)
		if err = rememberDownload(saveProfile, result, false); err != nil {
			return result, err
		}
	} else {
		file, err := openDownloadedFile(result.Path)
		if err != nil {
			return result, errors.New("文件已移动、删除或无法访问")
		}
		file.Close()
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			args := []string{"--", result.Path}
			if action == "reveal" {
				args = []string{"-R", "--", result.Path}
			}
			cmd = exec.Command("/usr/bin/open", args...)
		case "windows":
			if action == "reveal" {
				cmd = exec.Command("explorer.exe", "/select,"+result.Path)
			} else {
				cmd = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", result.Path)
			}
		default:
			path := result.Path
			if action == "reveal" {
				path = filepath.Dir(path)
			}
			cmd = exec.Command("gio", "open", path)
		}
		hideCommand(cmd)
		if err = cmd.Run(); err != nil {
			return result, errors.New("无法打开文件，请通过显示的位置手动查看")
		}
	}
	return result, nil
}

func saveDownloadedStream(destination string, source io.Reader, size int64) error {
	if existing, err := os.Lstat(destination); err == nil && !existing.Mode().IsRegular() {
		return errors.New("请选择普通文件保存位置")
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".cage-download-*.part")
	if err != nil {
		return errors.New("无法写入所选文件夹")
	}
	defer os.Remove(temporary.Name())
	count, err := io.Copy(temporary, source)
	if err == nil && size >= 0 && count != size {
		err = io.ErrUnexpectedEOF
	}
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return errors.New("保存未完成，原文件未覆盖；请检查空间或连接后重试")
	}
	if err = os.Rename(temporary.Name(), destination); err != nil {
		return errors.New("无法保存到所选位置，原文件未覆盖")
	}
	return nil
}
