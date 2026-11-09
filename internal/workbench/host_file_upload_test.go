package workbench

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func hostUploadFixture(t *testing.T, paths []string) []selectedHostFile {
	t.Helper()
	files, err := openSelectedHostFiles(paths)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, file := range files {
			file.file.Close()
		}
	})
	return files
}

func TestHostFileUploadPreservesBinaryNamesAndEmptyFiles(t *testing.T) {
	name := "参考视频 #+%.mp4"
	data := []byte{0, 255, 10, 13, 128, 0}
	first, second := filepath.Join(t.TempDir(), name), filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(first, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, nil, 0600); err != nil {
		t.Fatal(err)
	}
	files := hostUploadFixture(t, []string{first, second})
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/input/files/desktop/"+testBrowserFileID || r.URL.Query().Get("name") != name || r.Header.Get("Origin") != "http://"+r.Host {
			t.Errorf("unexpected request %s", r.URL)
		}
		body, err := io.ReadAll(r.Body)
		expected := data
		if calls == 1 {
			expected = nil
		}
		if err != nil || !bytes.Equal(body, expected) || r.ContentLength != int64(len(expected)) {
			t.Errorf("file bytes or size changed: %v", err)
		}
		calls++
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()
	if _, err := streamSelectedHostFiles(server.Client(), server.URL, "desktop", testBrowserFileID, files); err != nil || calls != 2 {
		t.Fatal(err, calls)
	}
}

func TestHostFileUploadRejectsFoldersLinksAndOversizeBeforeStreaming(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "large.mp4")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Truncate(browserFileLimit + 1); err != nil {
		t.Fatal(err)
	}
	file.Close()
	for _, paths := range [][]string{nil, {directory}, {path}, {"relative.txt"}, make([]string, 101)} {
		if files, err := openSelectedHostFiles(paths); err == nil {
			for _, file := range files {
				file.file.Close()
			}
			t.Fatalf("accepted invalid selection: %v", paths)
		}
	}
	link := filepath.Join(directory, "link")
	if err := os.Symlink(path, link); err == nil {
		if _, err := openSelectedHostFiles([]string{link}); err == nil {
			t.Fatal("accepted symbolic link")
		}
	}
}

func TestHostFileUploadStopsAfterRejectedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "one.txt")
	if err := os.WriteFile(path, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	files := hostUploadFixture(t, []string{path, path})
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusConflict)
	}))
	defer server.Close()
	if _, err := streamSelectedHostFiles(server.Client(), server.URL, "browser", testBrowserFileID, files); err == nil || calls != 1 {
		t.Fatal("continued a rejected batch", err, calls)
	}
}
