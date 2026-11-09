package workbench

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/sylvinus/agent-vm/internal/paths"
)

// Browser URLs use guest paths. The workbench's project and terminal actions
// use host paths, so an editor's workspace must identify its existing grant.
// Never infer a host drive or a new grant from an arbitrary guest path.
func (s *Server) browserStateForHost(body []byte) ([]byte, error) {
	var state map[string]json.RawMessage
	if err := json.Unmarshal(body, &state); err != nil {
		return nil, errors.New("浏览器状态无效")
	}
	// Cache completed default saves on the host so Open/Reveal continue to work
	// after the VM stops. A user-selected Save As location always wins.
	var downloads []map[string]json.RawMessage
	downloadsChanged := false
	if json.Unmarshal(state["downloads"], &downloads) == nil {
		root := s.environmentDir()
		for _, record := range downloads {
			encoded, _ := json.Marshal(record)
			var item browserDownloadInfo
			if json.Unmarshal(encoded, &item) != nil {
				continue
			}
			if item.State != "saved" || !browserFileID.MatchString(item.ID) || !browserDownloadName(item.Name) || item.Path != guestPath(filepath.Join(root, "Downloads", item.Name)) {
				continue
			}
			hostPath := filepath.Join(root, "Downloads", item.Name)
			if _, err := readSavedDownload(s.profile, item.ID); err != nil {
				_ = rememberDownload(s.profile, SavedDownload{ID: item.ID, Name: item.Name, Path: hostPath}, true)
			}
			if paths.Windows() && hostPath != item.Path {
				record["path"], _ = json.Marshal(hostPath)
				downloadsChanged = true
			}
		}
	}
	if !paths.Windows() {
		return body, nil
	}
	var tabs []map[string]json.RawMessage
	if err := json.Unmarshal(state["tabs"], &tabs); err != nil {
		return nil, errors.New("浏览器标签状态无效")
	}
	authorized, err := s.projectPaths()
	if err != nil {
		return nil, errors.New("无法读取项目列表")
	}
	byGuest := make(map[string]string, len(authorized))
	for _, hostPath := range authorized {
		byGuest[guestPath(hostPath)] = hostPath
	}
	changed := downloadsChanged
	for _, tab := range tabs {
		var kind, workspace string
		if json.Unmarshal(tab["kind"], &kind) != nil || kind != "editor" || json.Unmarshal(tab["workspace"], &workspace) != nil {
			continue
		}
		if hostPath, ok := byGuest[workspace]; ok && hostPath != workspace {
			tab["workspace"], _ = json.Marshal(hostPath)
			changed = true
		}
	}
	if !changed {
		return body, nil
	}
	if downloadsChanged {
		state["downloads"], _ = json.Marshal(downloads)
	}
	state["tabs"], err = json.Marshal(tabs)
	if err != nil {
		return nil, err
	}
	return json.Marshal(state)
}

// The guest emits one compact JSON snapshot per data line, plus heartbeat
// comments. Resolve grants for each snapshot so an already-open stream also
// recognizes projects added after it connected.
func (s *Server) writeBrowserEvents(ctx context.Context, w http.ResponseWriter, body io.Reader) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 16<<10), (1<<20)+16)
	controller := http.NewResponseController(w)
	eventName := ""
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Bytes()
		if bytes.HasPrefix(line, []byte("event: ")) {
			eventName = string(line[len("event: "):])
		}
		// Clipboard notifications are one-shot payloads, not tab snapshots.
		if bytes.HasPrefix(line, []byte("data: ")) && eventName != "clipboard" {
			mapped, err := s.browserStateForHost(line[len("data: "):])
			if err != nil {
				return err
			}
			line = append([]byte("data: "), mapped...)
		}
		if len(line) > browserResponseLimit+len("data: ") {
			return errors.New("浏览器事件超出大小限制")
		}
		_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := w.Write(append(line, '\n')); err != nil {
			return err
		}
		if len(line) == 0 {
			eventName = ""
			if err := controller.Flush(); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}
