package workbench

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sylvinus/agent-vm/internal/diagnostics"
	"github.com/sylvinus/agent-vm/internal/version"
)

// The desktop entry and selected workload have separate log ownership. Keep
// their source names while exporting both through the normal download route.
func diagnosticBundleWithDesktop(workload []byte, desktopProfile string) ([]byte, error) {
	var desktop bytes.Buffer
	if err := diagnostics.New(desktopProfile).Export(&desktop, version.Version, nil, "desktop connection service"); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	remaining := MaxDiagnosticBundle
	for _, source := range []struct {
		prefix string
		data   []byte
	}{{"", workload}, {"desktop/", desktop.Bytes()}} {
		reader, err := zip.NewReader(bytes.NewReader(source.data), int64(len(source.data)))
		if err != nil {
			return nil, errors.New("诊断包未完整生成，请重新导出")
		}
		seen := make(map[string]bool)
		for _, file := range reader.File {
			name := file.Name
			allowed := name == "manifest.json" || name == "README.txt" || name == "guest/services.log" || name == "safety/latest-report.json" ||
				strings.HasPrefix(name, "logs/") && path.Dir(name) == "logs" && strings.HasSuffix(name, ".jsonl")
			if !allowed || strings.ContainsAny(name, "\\\x00") || seen[name] || file.FileInfo().IsDir() || file.UncompressedSize64 > uint64(remaining) {
				return nil, errors.New("诊断包内容或大小不符合导出范围")
			}
			seen[name] = true
			input, err := file.Open()
			if err != nil {
				return nil, err
			}
			target, err := writer.Create(source.prefix + name)
			if err != nil {
				input.Close()
				return nil, err
			}
			written, copyErr := io.Copy(target, io.LimitReader(input, remaining+1))
			closeErr := input.Close()
			if copyErr != nil || closeErr != nil {
				return nil, errors.Join(copyErr, closeErr)
			}
			remaining -= written
			if remaining < 0 || output.Len() > int(MaxDiagnosticBundle) {
				return nil, errors.New("诊断包超过允许大小")
			}
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	if output.Len() > int(MaxDiagnosticBundle) {
		return nil, errors.New("诊断包超过允许大小")
	}
	return output.Bytes(), nil
}

func (s *Server) addDesktopDiagnosticResponse(response *http.Response) error {
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxDiagnosticBundle+1))
	response.Body.Close()
	if err != nil {
		return err
	}
	if int64(len(data)) > MaxDiagnosticBundle {
		return errors.New("诊断包超过允许大小")
	}
	data, err = diagnosticBundleWithDesktop(data, s.profile)
	if err != nil {
		return err
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	response.ContentLength = int64(len(data))
	response.Header.Set("Content-Length", strconv.Itoa(len(data)))
	return nil
}

// Plain CLI and offline exports address the workload directly. Include the
// associated desktop logs if that entry exists, without starting a controller.
func addDesktopDiagnosticFile(file *os.File, workloadProfile string) error {
	desktopProfile := filepath.Join(workloadProfile, "desktop-entry")
	if _, err := os.Lstat(filepath.Join(desktopProfile, "logs")); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxDiagnosticBundle+1))
	if err != nil || int64(len(data)) > MaxDiagnosticBundle {
		return errors.New("无法读取完整诊断包")
	}
	data, err = diagnosticBundleWithDesktop(data, desktopProfile)
	if err != nil {
		return err
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_, err = file.Write(data)
	return err
}
