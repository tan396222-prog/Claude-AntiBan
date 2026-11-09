package workbench

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sylvinus/agent-vm/internal/paths"
)

func windowsBrowserWorkspaceServer(t *testing.T) *Server {
	t.Helper()
	previous := paths.Windows
	paths.Windows = func() bool { return true }
	t.Cleanup(func() { paths.Windows = previous })
	s := testServer(t)
	s.ready = true
	s.preferences.Workspace = `C:\Users\甲 用户\项目 一`
	s.credentials.Token = "private-test-token"
	return s
}

func TestBrowserEditorWorkspaceRESTUsesAuthorizedWindowsProjects(t *testing.T) {
	s := windowsBrowserWorkspaceServer(t)
	second := `D:\跨盘 项目\two`
	if err := writePrivate(filepath.Join(s.profile, "projects.json"), sharedProjects{Paths: []string{second}}); err != nil {
		t.Fatal(err)
	}
	snapshot := `{"active":"first","back":true,"forward":false,"downloads":[{"id":"download","url":"https://example.test/a"}],"tabs":[` +
		`{"id":"first","kind":"editor","workspace":"/c/Users/甲 用户/项目 一","url":"http://127.0.0.1:18080/?folder=%2Fc%2FUsers%2F甲%20用户%2F项目%20一"},` +
		`{"id":"second","kind":"editor","workspace":"/d/跨盘 项目/two","url":"http://127.0.0.1:18080/?folder=%2Fd%2F跨盘%20项目%2Ftwo"},` +
		`{"id":"web","kind":"web","favicon":"data:image/png;base64,aWNvbg==","workspace":"/c/Users/甲 用户/项目 一","url":"https://example.test/"},` +
		`{"id":"unshared","kind":"editor","workspace":"/e/private","url":"http://127.0.0.1:18080/?folder=%2Fe%2Fprivate"}]}`
	s.browserControl = browserClientForTest(t, s, browserTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || r.URL.Path != "/tabs" || r.Header.Get("Authorization") != "Bearer private-test-token" {
			t.Fatalf("unexpected guest snapshot request: %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(snapshot))}, nil
	}))
	w := request(s, "GET", "/api/browser/tabs", "", "", true)
	if w.Code != http.StatusOK {
		t.Fatalf("snapshot: %d %s", w.Code, w.Body.String())
	}
	var got, want map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(snapshot), &want); err != nil {
		t.Fatal(err)
	}
	wantTabs := want["tabs"].([]any)
	wantTabs[0].(map[string]any)["workspace"] = s.workspace()
	wantTabs[1].(map[string]any)["workspace"] = second
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot changed a URL/non-editor/unshared path or lost a granted host path:\ngot: %s", w.Body.String())
	}
	if _, err := s.projectWorkspace("/e/private"); err == nil {
		t.Fatal("an unshared editor path became a host project grant")
	}
	stream := httptest.NewRecorder()
	if err := s.writeBrowserEvents(context.Background(), stream, strings.NewReader(": heartbeat\n\ndata: "+snapshot+"\n\n")); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stream.Body.String(), ": heartbeat\n\n") {
		t.Fatal("SSE heartbeat was lost")
	}
	lines := strings.Split(stream.Body.String(), "\n")
	var event map[string]any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[2], "data: ")), &event); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(event, got) {
		t.Fatal("SSE and REST expose different editor workspaces")
	}
	copyFrame := "event: clipboard\ndata: {\"id\":\"web\",\"text\":\"复制🙂\\n第二行\",\"context\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"}\n\n"
	stream = httptest.NewRecorder()
	if err := s.writeBrowserEvents(context.Background(), stream, strings.NewReader(copyFrame+"data: "+snapshot+"\n\n")); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stream.Body.String(), copyFrame) {
		t.Fatal("clipboard event was changed or treated as a Windows tab snapshot")
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(stream.Body.String()[len(copyFrame):], "data: "))), &event); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(event, got) {
		t.Fatal("clipboard event prevented the next snapshot from mapping Windows workspaces")
	}
}

type browserGrantReader struct {
	reader io.Reader
	next   func() io.Reader
}

func (r *browserGrantReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if err == io.EOF && r.next != nil {
		r.reader, r.next = r.next(), nil
		return r.reader.Read(p)
	}
	return n, err
}

func TestBrowserEditorWorkspaceSSESeesProjectsAddedAfterConnection(t *testing.T) {
	s := windowsBrowserWorkspaceServer(t)
	second := `D:\跨盘 项目\two`
	payload := `{"tabs":[{"kind":"editor","workspace":"/d/跨盘 项目/two","url":"http://127.0.0.1:18080/?folder=%2Fd%2F跨盘%20项目%2Ftwo"}]}`
	frame := "data: " + payload + "\n\n"
	reader := &browserGrantReader{reader: strings.NewReader(frame), next: func() io.Reader {
		if err := writePrivate(filepath.Join(s.profile, "projects.json"), sharedProjects{Paths: []string{second}}); err != nil {
			t.Fatal(err)
		}
		return strings.NewReader(frame)
	}}
	w := httptest.NewRecorder()
	if err := s.writeBrowserEvents(context.Background(), w, reader); err != nil {
		t.Fatal(err)
	}
	var workspaces []string
	scanner := bufio.NewScanner(strings.NewReader(w.Body.String()))
	for scanner.Scan() {
		if !strings.HasPrefix(scanner.Text(), "data: ") {
			continue
		}
		var state struct{ Tabs []struct{ Workspace string } }
		if err := json.Unmarshal([]byte(strings.TrimPrefix(scanner.Text(), "data: ")), &state); err != nil {
			t.Fatal(err)
		}
		workspaces = append(workspaces, state.Tabs[0].Workspace)
	}
	if !reflect.DeepEqual(workspaces, []string{"/d/跨盘 项目/two", second}) {
		t.Fatalf("SSE cached or guessed project grants: %#v", workspaces)
	}
}
