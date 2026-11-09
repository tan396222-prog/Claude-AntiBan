package workbench

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileInputBrowserGestureAuthorizationAndTarget(t *testing.T) {
	s := testServer(t)
	s.ready = true
	s.credentials.Token = "guest-token"
	calls := 0
	s.browserControl = browserClientForTest(t, s, browserTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["id"] != "chromium-tab" || body["kind"] != "drop" || body["x"] != 0.25 || r.URL.Path != "/upload-start" || r.Header.Get("Authorization") != "Bearer guest-token" {
			t.Fatal("gesture changed", body, r.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"` + testBrowserFileID + `"}`))}, nil
	}))
	body := `{"target":"browser","id":"chromium-tab","kind":"drop","x":0.25,"y":0.75}`
	path := "/api/input/files/start"
	for _, c := range []struct {
		origin string
		auth   bool
		code   int
	}{{s.origin, false, 401}, {"https://example.com", true, 403}, {s.origin, true, 200}} {
		if w := request(s, "POST", path, c.origin, body, c.auth); w.Code != c.code {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	s.account.lock()
	if w := request(s, "POST", path, s.origin, body, true); w.Code != 423 || calls != 1 {
		t.Fatal("locked gesture reached guest", w.Code, calls)
	}
}

func TestDesktopDownloadsRegisterOnlyCompletedFilesInSharedDownloadDirectory(t *testing.T) {
	s := testServer(t)
	name := "视频.mp4"
	item := map[string]any{"id": testBrowserFileID, "name": name, "state": "saved",
		"path": strings.TrimRight(guestPath(s.environmentDir()), "/") + "/Downloads/" + name}
	result := map[string]any{"downloads": []any{item}}
	if err := s.rememberDesktopDownloads(result); err != nil {
		t.Fatal(err)
	}
	saved, err := readSavedDownload(s.profile, testBrowserFileID)
	if err != nil || saved.Path != filepath.Join(s.environmentDir(), "Downloads", name) {
		t.Fatal(saved, err)
	}
	// A user-chosen host destination must survive later guest polling.
	saved.Path = filepath.Join(t.TempDir(), name)
	if err := rememberDownload(s.profile, saved, false); err != nil {
		t.Fatal(err)
	}
	item["path"] = strings.TrimRight(guestPath(s.environmentDir()), "/") + "/Downloads/" + name
	if err := s.rememberDesktopDownloads(result); err != nil {
		t.Fatal(err)
	}
	again, _ := readSavedDownload(s.profile, testBrowserFileID)
	if again.Path != saved.Path {
		t.Fatal("poll replaced user destination", again)
	}
	for _, path := range []string{"/etc/passwd", strings.TrimRight(guestPath(s.environmentDir()), "/") + "/Downloads/../secret"} {
		item["path"] = path
		if s.rememberDesktopDownloads(result) == nil {
			t.Fatal("untrusted path accepted", path)
		}
	}
	item["name"] = "../secret"
	if s.rememberDesktopDownloads(result) == nil {
		t.Fatal("path-like name accepted")
	}
	item["name"], item["state"] = name, "downloading"
	if err := os.Remove(savedDownloadPath(s.profile, testBrowserFileID)); err != nil {
		t.Fatal(err)
	}
	if err := s.rememberDesktopDownloads(result); err != nil {
		t.Fatal(err)
	}
	if _, err := readSavedDownload(s.profile, testBrowserFileID); err == nil {
		t.Fatal("partial file registered")
	}
	if _, exists := item["path"]; exists {
		t.Fatal("partial host path leaked")
	}
}

func TestFileInputTerminalRequiresOriginalLiveSession(t *testing.T) {
	s := testServer(t)
	s.ready = true
	id := "1234567890abcdef"
	terminal := newTerminalSession(nil)
	terminal.id = id
	s.terminals.sessions = map[string]*terminalSession{id: terminal}
	body := `{"target":"terminal","id":"` + id + `","kind":"paste"}`
	if w := request(s, "POST", "/api/input/files/start", s.origin, body, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	terminal.closed = true
	if w := request(s, "POST", "/api/input/files/start", s.origin, body, true); w.Code != 409 {
		t.Fatal("closed terminal accepted files", w.Code)
	}
	for _, target := range []string{"terminal", "browser", "desktop", "host"} {
		if w := request(s, "POST", "/api/input/files/"+target+"/invalid?name=file.mp4", s.origin, "bytes", true); w.Code < 400 {
			t.Fatal("invalid upload accepted", target)
		}
	}
}

func TestDesktopDirectorySelectionRequiresAuthorizedProject(t *testing.T) {
	s := testServer(t)
	s.ready = true
	workspace, _ := json.Marshal(t.TempDir())
	w := request(s, "POST", "/api/input/files/directory", s.origin,
		`{"target":"desktop","id":"`+testBrowserFileID+`","workspace":`+string(workspace)+`}`, true)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "请先通过打开项目") {
		t.Fatal("existing unshared directory accepted", w.Code, w.Body.String())
	}
	for _, body := range []string{
		`{"target":"desktop","id":"` + testBrowserFileID + `","workspace":"/unshared-project"}`,
		`{"target":"desktop","id":"` + testBrowserFileID + `"}`,
		`{"target":"browser","id":"` + testBrowserFileID + `","workspace":"/unshared-project"}`,
		`{"target":"terminal","id":"1234567890abcdef","workspace":"/unshared-project"}`,
	} {
		w := request(s, "POST", "/api/input/files/directory", s.origin, body, true)
		if w.Code < 400 || w.Code >= 500 {
			t.Fatal("unapproved directory reached guest", w.Code, w.Body.String())
		}
	}
}
