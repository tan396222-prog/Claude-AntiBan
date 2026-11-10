package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const testBrowserFileID = "34e399c1-78fe-4cf1-93b0-d7fef431b211"

func TestBrowserUploadStreamsOnlyToLiveChooser(t *testing.T) {
	s := testServer(t)
	s.ready = true
	s.credentials.Token = "guest-token"
	data := bytes.Repeat([]byte{0, 1, 255, 0, 127}, 1<<18)
	posts := 0
	s.browserControl = browserClientForTest(t, s, browserTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer guest-token" {
			t.Fatal("missing guest authentication")
		}
		body := `{"uploads":[{"id":"` + testBrowserFileID + `"}]}`
		if r.Method == "POST" {
			posts++
			if r.URL.Path != "/upload-file" || r.URL.Query().Get("id") != testBrowserFileID || r.URL.Query().Get("name") != "参考视频.mp4" {
				t.Fatal("wrong upload target", r.URL)
			}
			got, err := io.ReadAll(r.Body)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatal("binary bytes changed", err)
			}
			body = `{"ok":true}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}))
	call := func(origin string, auth bool, name string, size int64) int {
		r := httptest.NewRequest("POST", s.origin+"/api/browser/files/uploads/"+testBrowserFileID+"?name="+name, bytes.NewReader(data))
		r.Host = "localhost:17777"
		r.Header.Set("Origin", origin)
		r.ContentLength = size
		if auth {
			r.AddCookie(&http.Cookie{Name: s.cookieName(), Value: s.session})
		}
		w := httptest.NewRecorder()
		s.handler().ServeHTTP(w, r)
		return w.Code
	}
	if code := call(s.origin, false, "video.mp4", int64(len(data))); code != 401 {
		t.Fatal(code)
	}
	if code := call("https://example.com", true, "video.mp4", int64(len(data))); code != 403 {
		t.Fatal(code)
	}
	if code := call(s.origin, true, "../private", 1); code != 400 {
		t.Fatal(code)
	}
	if code := call(s.origin, true, "video.mp4", browserFileLimit+1); code != 413 {
		t.Fatal(code)
	}
	if posts != 0 {
		t.Fatal("rejected stream reached guest")
	}
	if code := call(s.origin, true, "%E5%8F%82%E8%80%83%E8%A7%86%E9%A2%91.mp4", int64(len(data))); code != 200 || posts != 1 {
		t.Fatal(code, posts)
	}
}

type failedDownloadReader struct{}

func (failedDownloadReader) Read([]byte) (int, error) { return 0, errors.New("interrupted") }

func TestSaveDownloadNeverPublishesPartialOrClobbersOnFailure(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "video.mp4")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, reader := range []io.Reader{failedDownloadReader{}, strings.NewReader("short")} {
		if err := saveDownloadedStream(path, reader, 100); err == nil {
			t.Fatal("partial save accepted")
		}
		got, _ := os.ReadFile(path)
		if string(got) != "original" {
			t.Fatal("original overwritten")
		}
	}
	data := bytes.Repeat([]byte{0, 255, 77}, 1<<19)
	if err := saveDownloadedStream(path, bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, data) {
		t.Fatal("saved content changed")
	}
	files, _ := filepath.Glob(filepath.Join(directory, ".cage-download-*.part"))
	if len(files) != 0 {
		t.Fatal("partial files retained")
	}
}

func TestSavedDownloadLocationSurvivesDefaultSnapshot(t *testing.T) {
	profile := t.TempDir()
	chosen := SavedDownload{ID: testBrowserFileID, Name: "movie.mp4", Path: filepath.Join(t.TempDir(), "movie.mp4")}
	if err := rememberDownload(profile, chosen, false); err != nil {
		t.Fatal(err)
	}
	fallback := chosen
	fallback.Path = filepath.Join(t.TempDir(), "fallback.mp4")
	if err := rememberDownload(profile, fallback, true); err != nil {
		t.Fatal(err)
	}
	got, err := readSavedDownload(profile, testBrowserFileID)
	if err != nil || got.Path != chosen.Path {
		t.Fatal("chosen destination lost", err)
	}
	if _, err := readSavedDownload(profile, "../outside"); err == nil {
		t.Fatal("path escape accepted")
	}
}

func TestCompletedDownloadsRemainAvailableAfterVMStops(t *testing.T) {
	s := testServer(t)
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = writePrivate(filepath.Join(s.profile, "environment.json"), map[string]string{"directory": directory}); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(directory, "Downloads"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "Downloads", "video.mp4")
	data := []byte{0, 1, 255, 0, 127}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := json.Marshal(map[string]any{"tabs": []any{}, "downloads": []browserDownloadInfo{{ID: testBrowserFileID, Name: "video.mp4", State: "saved", Path: guestPath(path)}}})
	if _, err = s.browserStateForHost(snapshot); err != nil {
		t.Fatal(err)
	}
	s.ready = false
	route := "/api/browser/files/downloads/" + testBrowserFileID + "/content"
	w := request(s, "GET", route, "", "", true)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data) {
		t.Fatal(w.Code, w.Body.String())
	}
	s.account.lock()
	if w = request(s, "GET", route, "", "", true); w.Code != 423 {
		t.Fatal("locked file read accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reader := cancelableDownload{ReadSeeker: bytes.NewReader(data), ctx: ctx}
	if _, err = reader.Read(make([]byte, 16)); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled file read continued", err)
	}
}

func TestNativeSaveUsesAuthenticatedControllerAndPersistsChosenPath(t *testing.T) {
	s := testServer(t)
	lock, err := openControllerLock(s.profile, os.O_CREATE|os.O_RDWR)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := lockController(lock); err != nil {
		t.Fatal(err)
	}
	defer unlockController(lock)
	server := httptest.NewServer(s.handler())
	defer server.Close()
	endpoint, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(endpoint.Port())
	s.origin = "http://localhost:" + endpoint.Port()
	if err := writePrivate(filepath.Join(s.profile, "control.json"), controlFile{Port: port, Token: s.token, PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(directory, "source.mp4")
	destination := filepath.Join(directory, "另存视频.mp4")
	data := bytes.Repeat([]byte{0, 255, 42, 77}, 1<<18)
	if err = os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = rememberDownload(s.profile, SavedDownload{ID: testBrowserFileID, Name: "source.mp4", Path: source}, false); err != nil {
		t.Fatal(err)
	}
	result, err := BrowserFile(s.configPath, "save", testBrowserFileID, destination)
	if err != nil || result.Path != destination {
		t.Fatal(result, err)
	}
	saved, _ := os.ReadFile(destination)
	original, _ := os.ReadFile(source)
	if !bytes.Equal(saved, data) || !bytes.Equal(original, data) {
		t.Fatal("save changed source or bytes")
	}
	record, err := readSavedDownload(s.profile, testBrowserFileID)
	if err != nil || record.Path != destination {
		t.Fatal("chosen path not persisted", err)
	}
	s.account.lock()
	refused := filepath.Join(directory, "locked.mp4")
	if _, err = BrowserFile(s.configPath, "save", testBrowserFileID, refused); err == nil {
		t.Fatal("locked native export accepted")
	}
	if _, err = os.Stat(refused); !os.IsNotExist(err) {
		t.Fatal("locked export wrote a file")
	}
}
