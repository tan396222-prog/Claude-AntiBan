package workbench

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

type selectedHostFile struct {
	file *os.File
	name string
	size int64
}

func openSelectedHostFiles(paths []string) ([]selectedHostFile, error) {
	if len(paths) == 0 || len(paths) > 100 {
		return nil, errors.New("请选择 1 至 100 个文件")
	}
	files := make([]selectedHostFile, 0, len(paths))
	closeFiles := func() {
		for _, file := range files {
			file.file.Close()
		}
	}
	for _, path := range paths {
		if !filepath.IsAbs(path) || !browserFileName(filepath.Base(path)) {
			closeFiles()
			return nil, errors.New("所选文件路径或名称无效")
		}
		before, err := os.Lstat(path)
		if err != nil || !before.Mode().IsRegular() {
			closeFiles()
			return nil, errors.New("请选择本机普通文件；文件夹、链接或已删除的文件无法上传")
		}
		file, err := os.Open(path)
		if err != nil {
			closeFiles()
			return nil, errors.New("无法读取所选文件，请检查文件权限")
		}
		after, err := file.Stat()
		if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) || after.Size() > browserFileLimit {
			file.Close()
			closeFiles()
			return nil, errors.New("所选文件已变化或超过 2 GB，请重新选择")
		}
		files = append(files, selectedHostFile{file: file, name: filepath.Base(path), size: after.Size()})
	}
	return files, nil
}

func streamSelectedHostFiles(client *http.Client, origin, target, id string, files []selectedHostFile) ([]string, error) {
	paths := []string{}
	for _, file := range files {
		path := origin + "/api/input/files/" + target + "/" + id + "?" + url.Values{"name": {file.name}}.Encode()
		request, err := http.NewRequest("POST", path, io.NewSectionReader(file.file, 0, file.size))
		if err != nil {
			return nil, errors.New("文件传递请求无效")
		}
		request.Header.Set("Origin", origin)
		request.Header.Set("Content-Type", "application/octet-stream")
		request.ContentLength = file.size
		if file.size == 0 {
			request.Body = http.NoBody
		}
		response, err := client.Do(request)
		if err != nil {
			return nil, errors.New("文件传递中断，请重新选择文件后重试")
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, 4097))
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("文件传递失败（%d），请重新发起上传", response.StatusCode)
		}
		var result struct {
			OK   bool   `json:"ok"`
			Path string `json:"path"`
		}
		if readErr != nil || len(data) > 4096 || json.Unmarshal(data, &result) != nil || !result.OK {
			return nil, errors.New("文件传递未返回有效结果，请重试")
		}
		if result.Path != "" {
			paths = append(paths, result.Path)
		}
	}
	return paths, nil
}

// UploadHostFiles is a native-only entry point. Paths come from an OS picker or
// a user-initiated clipboard snapshot, never an HTTP request or a guest page.
func UploadHostFiles(configPath, target, id string, paths []string) (map[string]any, error) {
	return UploadHostFilesForConnection(configPath, "", target, id, paths)
}

func UploadHostFilesForConnection(configPath, connectionID, target, id string, paths []string) (map[string]any, error) {
	if (target != "browser" && target != "desktop" && target != "terminal") || !validFileTargetID(target, id) {
		return nil, errors.New("文件上传目标无效")
	}
	files, err := openSelectedHostFiles(paths)
	if err != nil {
		return nil, err
	}
	defer func() {
		for _, file := range files {
			file.file.Close()
		}
	}()
	client, err := connectController(filepath.Dir(configPath))
	if err != nil {
		return nil, err
	}
	defer client.close()
	entry, _ := url.Parse(client.launch.URL)
	status, err := client.call("POST", "/api/session", map[string]string{"token": entry.Fragment}, nil)
	if err != nil || status != http.StatusOK {
		return nil, errors.New("无法连接文件上传会话")
	}
	if _, err = client.bindConnection(connectionID); err != nil {
		return nil, err
	}
	client.client.Timeout = 30 * time.Minute
	pathsResult, err := streamSelectedHostFiles(client.client, client.origin, target, id, files)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "count": len(files), "paths": pathsResult}, nil
}
