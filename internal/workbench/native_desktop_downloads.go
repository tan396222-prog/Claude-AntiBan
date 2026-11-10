package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

func nativeDesktopDownloadInput(method, path string, input any) (map[string]any, error) {
	if method != "POST" || (path != "/file-open" && path != "/file-read" && path != "/file-close") {
		return nil, errors.New("客机下载操作无效")
	}
	data, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	var request map[string]any
	if err = json.Unmarshal(data, &request); err != nil {
		return nil, err
	}
	request["action"] = "files-download-" + strings.TrimPrefix(path, "/file-")
	return request, nil
}

func (s *Server) nativeDesktopDownloadRequest(ctx context.Context, method, path string, input any) ([]byte, error) {
	request, err := nativeDesktopDownloadInput(method, path, input)
	if err != nil {
		return nil, err
	}
	if s.windowsGuest != nil {
		var reply map[string]any
		if err = s.windowsDesktopRequest(ctx, request["action"].(string), request, &reply); err != nil {
			return nil, windowsDesktopFileFailure(err)
		}
		return json.Marshal(reply)
	}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	var mac macOSWindowRequest
	if err = json.Unmarshal(data, &mac); err != nil {
		return nil, err
	}
	reply, err := s.macOSDesktopFileCall(ctx, mac)
	if err != nil {
		return nil, err
	}
	return json.Marshal(reply)
}

type nativeDesktopDownload struct {
	io.ReadCloser
	connection *nativeWindowConnection
}

func (d *nativeDesktopDownload) Close() error {
	err := d.ReadCloser.Close()
	d.connection.Close()
	return err
}

// One download owns one ordered window channel. Chunk reads reuse its process
// and SSH or Hyper-V transport, while the producer retains the file identity.
func (s *Server) openNativeDesktopDownload(ctx context.Context, id string) (io.ReadCloser, int64, string, error) {
	var connection *nativeWindowConnection
	var err error
	if s.windowsGuest != nil {
		connection, err = s.windowsWindowConnection(ctx)
	} else {
		transport, cancel := context.WithCancel(ctx)
		cmd := s.sshCommand(transport, "-T", s.sshTarget(), "--", macOSWindowSocketCommand)
		connection, err = startWindowConnection(cmd, cancel)
	}
	if err != nil {
		return nil, 0, "", err
	}
	request := func(call context.Context, method, path string, input any) ([]byte, error) {
		if path == "/file-close" && ctx.Err() != nil {
			return s.nativeDesktopDownloadRequest(call, method, path, input)
		}
		if err := call.Err(); err != nil {
			return nil, err
		}
		payload, err := nativeDesktopDownloadInput(method, path, input)
		if err != nil {
			return nil, err
		}
		payload["keepAlive"] = true
		stop := context.AfterFunc(call, connection.Close)
		defer stop()
		var reply json.RawMessage
		if err = connection.request(payload, &reply); err != nil {
			var failure windowsWindowFailure
			if s.macosGuest != nil && errors.As(err, &failure) {
				return nil, macOSFileFailure(failure.code)
			}
			return nil, windowsDesktopFileFailure(err)
		}
		return reply, nil
	}
	reader, size, name, err := openNativeFileDownload(ctx, id, request)
	if err != nil {
		connection.Close()
		return nil, 0, "", err
	}
	return &nativeDesktopDownload{ReadCloser: reader, connection: connection}, size, name, nil
}

func desktopDownloadFailure(reason string) error {
	switch reason {
	case "desktop_download_incomplete":
		return errors.New("Claude Desktop 尚未完整保存文件，请回到应用重新下载")
	case "desktop_download_changed":
		return errors.New("Claude Desktop 下载文件在传输中发生变化，请重新下载")
	case "desktop_download_unavailable", "desktop_download_transfer_invalid":
		return errors.New("Claude Desktop 下载文件已不可用，请确认客机已解锁后重新下载")
	case "desktop_download_limit":
		return errors.New("待保存的 Claude Desktop 文件过多，请先完成现有下载后重试")
	case "file_open_target_changed":
		return errors.New("文件或 Claude 窗口已变化，请回到原窗口重新打开文件")
	case "file_open_request_invalid", "file_open_request_unavailable":
		return errors.New("文件打开请求已失效，请确认客机已解锁后重新操作")
	case "file_open_request_limit":
		return errors.New("正在处理的文件打开请求过多，请稍后重试")
	default:
		return nil
	}
}

func windowsDesktopFileFailure(err error) error {
	var failure windowsWindowFailure
	if errors.As(err, &failure) {
		if display := desktopDownloadFailure(failure.code); display != nil {
			return display
		}
	}
	return err
}
