package workbench

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/guestos"
	"github.com/sylvinus/agent-vm/internal/remotehost"
)

// The child provides the native controller protocol over a real pipe. Every
// download source is a test-owned file selected by its catalog ID.
func TestNativeDownloadRelay(t *testing.T) {
	directory := os.Getenv("CAGE_NATIVE_DOWNLOAD_FIXTURE")
	if directory == "" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	sources := make(map[string][]byte)
	for scanner.Scan() {
		var request struct {
			Path  string
			Input struct {
				ID, Transfer string
				Sequence     int
				After        int
			}
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(2)
		}
		raw, err := os.ReadFile(filepath.Join(directory, "catalog.json"))
		if err != nil {
			os.Exit(3)
		}
		var state struct{ Downloads []browserDownloadInfo }
		if json.Unmarshal(raw, &state) != nil {
			os.Exit(4)
		}
		var result any = map[string]any{"ok": true}
		switch request.Path {
		case "/tabs":
			result = map[string]any{"active": "tab", "tabs": []any{}, "downloads": state.Downloads}
		case "/events":
			result = map[string]any{"sequence": 1}
			if request.Input.After != 1 {
				result = map[string]any{"sequence": 1, "state": map[string]any{"active": "tab", "tabs": []any{}, "downloads": state.Downloads}}
			}
		case "/file-open":
			var selected *browserDownloadInfo
			for i := range state.Downloads {
				if state.Downloads[i].ID == request.Input.ID {
					selected = &state.Downloads[i]
				}
			}
			if selected == nil {
				os.Exit(5)
			}
			source, err := os.ReadFile(filepath.Join(directory, selected.ID))
			if err != nil {
				os.Exit(6)
			}
			transfer := strings.ReplaceAll(selected.ID, "-", "")
			sources[transfer] = source
			result = map[string]any{"transfer": transfer, "size": len(source), "name": selected.Name}
		case "/file-read":
			_ = os.WriteFile(filepath.Join(directory, "reading"), nil, 0600)
			for {
				if _, err := os.Stat(filepath.Join(directory, "block")); os.IsNotExist(err) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if _, err := os.Stat(filepath.Join(directory, "fail")); err == nil {
				os.Exit(7)
			}
			source := sources[request.Input.Transfer]
			start := request.Input.Sequence * (64 << 10)
			end := min(start+(64<<10), len(source))
			if start > len(source) {
				os.Exit(8)
			}
			result = map[string]any{"data": base64.StdEncoding.EncodeToString(source[start:end]), "sequence": request.Input.Sequence + 1, "eof": end == len(source)}
		}
		if json.NewEncoder(os.Stdout).Encode(result) != nil {
			os.Exit(9)
		}
	}
	os.Exit(0)
}

func nativeDownloadFixture(t *testing.T, system string, records []browserDownloadInfo, payload []byte) (*Server, string, string) {
	t.Helper()
	s := testServer(t)
	if system == "macos" {
		s.macosGuest = &guestos.MacOSProfile{Instance: "mac", User: "cage"}
	} else {
		s.windowsGuest = &guestos.WindowsProfile{}
	}
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = writePrivate(filepath.Join(s.profile, "environment.json"), map[string]string{"directory": workspace}); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	catalog, _ := json.Marshal(map[string]any{"downloads": records})
	if err = os.WriteFile(filepath.Join(directory, "catalog.json"), catalog, 0600); err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if err = os.WriteFile(filepath.Join(directory, record.ID), payload, 0600); err != nil {
			t.Fatal(err)
		}
	}
	client := newWindowsBrowserClient(context.Background(), "unused-native-download-fixture")
	client.start = func(ctx context.Context) (*exec.Cmd, error) {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeDownloadRelay$")
		cmd.Env = append(os.Environ(), "CAGE_NATIVE_DOWNLOAD_FIXTURE="+directory)
		return cmd, nil
	}
	s.windowsBrowserControl = client
	s.ready = true
	t.Cleanup(func() { s.beginClose(); client.close(); s.activities.Wait() })
	return s, workspace, directory
}

func TestNativeDownloadsPublishHostCopiesAndPreserveSavedLocations(t *testing.T) {
	for _, system := range []string{"macos", "windows-11"} {
		t.Run(system, func(t *testing.T) {
			ids := []string{uuid.NewString(), uuid.NewString()}
			records := []browserDownloadInfo{
				{ID: ids[0], Name: "参考.bin", State: "saved", Path: "Downloads/参考.bin"},
				{ID: ids[1], Name: "参考.bin", State: "saved", Path: "/untrusted/guest/path"},
			}
			payload := bytes.Repeat([]byte{0, 1, 127, 255}, 40001)
			s, workspace, _ := nativeDownloadFixture(t, system, records, payload)
			if err := os.Mkdir(filepath.Join(workspace, "Downloads"), 0700); err != nil {
				t.Fatal(err)
			}
			existing := filepath.Join(workspace, "Downloads", "参考.bin")
			if err := os.WriteFile(existing, []byte("existing"), 0600); err != nil {
				t.Fatal(err)
			}
			snapshot, _ := json.Marshal(map[string]any{"tabs": []any{}, "downloads": records})
			if _, err := s.browserStateForHost(snapshot); err != nil {
				t.Fatal(err)
			}
			var workers sync.WaitGroup
			paths := make(chan string, 8)
			for n := 0; n < 8; n++ {
				id := ids[n%len(ids)]
				workers.Add(1)
				go func() {
					defer workers.Done()
					item, err := s.downloadInfo(t.Context(), id)
					if err != nil {
						t.Error(err)
						return
					}
					paths <- item.Path
				}()
			}
			workers.Wait()
			close(paths)
			distinct := map[string]bool{}
			for path := range paths {
				distinct[path] = true
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, payload) {
					t.Errorf("host copy differs: %v", err)
				}
			}
			if len(distinct) != 2 {
				t.Fatalf("duplicate requests created %d copies", len(distinct))
			}
			original, _ := os.ReadFile(existing)
			if string(original) != "existing" {
				t.Fatal("existing same-name file overwritten")
			}
			mapped, err := s.browserStateForHost(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			var state struct{ Downloads []browserDownloadInfo }
			if json.Unmarshal(mapped, &state) != nil {
				t.Fatal("invalid host state")
			}
			for _, record := range state.Downloads {
				if record.State != "saved" || !filepath.IsAbs(record.Path) || !strings.HasPrefix(record.Path, workspace+string(filepath.Separator)) {
					t.Fatalf("not a completed host location: %#v", record)
				}
			}
			chosen := filepath.Join(workspace, "chosen.bin")
			if err := saveDownloadedStream(chosen, bytes.NewReader(payload), int64(len(payload))); err != nil {
				t.Fatal(err)
			}
			if err := rememberDownload(s.profile, SavedDownload{ID: ids[0], Name: "chosen.bin", Path: chosen}, false); err != nil {
				t.Fatal(err)
			}
			if _, err := s.browserStateForHost(snapshot); err != nil {
				t.Fatal(err)
			}
			s.ready = false
			response := windowsBrowserRequestForTest(s, http.MethodGet, "/api/browser/files/downloads/"+ids[0]+"/content", nil)
			if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), payload) {
				t.Fatalf("offline saved copy unavailable: %d", response.Code)
			}
			saved, err := readSavedDownload(s.profile, ids[0])
			if err != nil || saved.Path != chosen {
				t.Fatal("explicit saved location replaced")
			}
		})
	}
}

func TestNativeDownloadFailureRetryAndCancellation(t *testing.T) {
	for _, mode := range []string{"failed-read", "account-lock", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			id := uuid.NewString()
			item := browserDownloadInfo{ID: id, Name: "retry.txt", State: "saved", Path: "Downloads/retry.txt"}
			payload := bytes.Repeat([]byte("test payload"), 10000)
			s, workspace, directory := nativeDownloadFixture(t, "macos", []browserDownloadInfo{item}, payload)
			switch mode {
			case "failed-read":
				if err := os.WriteFile(filepath.Join(directory, "fail"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "account-lock":
				if err := os.WriteFile(filepath.Join(directory, "block"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(t.TempDir(), filepath.Join(workspace, "Downloads")); err != nil {
					t.Skip(err)
				}
			}
			transfer, err := s.startNativeDownload(item)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "account-lock" {
				deadline := time.Now().Add(5 * time.Second)
				for {
					if _, err := os.Stat(filepath.Join(directory, "reading")); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("reader did not start")
					}
					time.Sleep(10 * time.Millisecond)
				}
				s.account.lock()
			}
			select {
			case <-transfer.done:
			case <-time.After(5 * time.Second):
				t.Fatal("failed transfer was not retired")
			}
			if transfer.err == nil {
				t.Fatal("failed transfer published success")
			}
			if _, err := readSavedDownload(s.profile, id); err == nil {
				t.Fatal("failed download has saved receipt")
			}
			files, _ := filepath.Glob(filepath.Join(workspace, "Downloads", "*"))
			if len(files) != 0 {
				t.Fatal("failed download left a host file", files)
			}
			if mode == "symlink" {
				return
			}
			_ = os.Remove(filepath.Join(directory, "fail"))
			_ = os.Remove(filepath.Join(directory, "block"))
			if mode == "account-lock" {
				s.account.setKey(make([]byte, 32))
			}
			response := windowsBrowserRequestForTest(s, "POST", "/api/browser/download-retry", []byte(`{"id":"`+id+`"}`))
			if response.Code != 200 {
				t.Fatalf("retry refused: %d %s", response.Code, response.Body.String())
			}
			saved, err := s.downloadInfo(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(saved.Path)
			if !bytes.Equal(got, payload) {
				t.Fatal("retry did not complete host copy")
			}
		})
	}
}

func TestNativeBrowserFileSaveUsesProductionHostBridge(t *testing.T) {
	item := browserDownloadInfo{ID: uuid.NewString(), Name: "download.bin", State: "saved", Path: "Downloads/download.bin"}
	payload := bytes.Repeat([]byte{0, 255, 42}, 50000)
	s, workspace, _ := nativeDownloadFixture(t, "windows-11", []browserDownloadInfo{item}, payload)
	workload := httptest.NewServer(s.handler())
	defer workload.Close()
	endpoint, _ := url.Parse(workload.URL)
	s.origin = "http://localhost:" + endpoint.Port()
	workloadURL, _ := url.Parse(s.origin)
	jar, _ := cookiejar.New(nil)
	jar.SetCookies(workloadURL, []*http.Cookie{
		{Name: s.cookieName(), Value: s.session, Path: "/"},
		{Name: s.cookieName() + "-desktop", Value: trustedViewProof(s.token), Path: "/"},
	})
	session, err := remotehost.LocalSession(t.Context(), s.origin, remotehost.PublicManifest{InstanceID: strings.Repeat("a", 64)}, jar)
	if err != nil {
		t.Fatal(err)
	}
	gateway := testServer(t)
	gateway.profile = filepath.Join(s.profile, "desktop-entry")
	gateway.configPath = filepath.Join(gateway.profile, "config.json")
	if err = os.MkdirAll(gateway.profile, 0700); err != nil {
		t.Fatal(err)
	}
	gateway.remote = newDesktopGateway(gateway.profile, nil)
	generation := gateway.remote.desktop.generation
	ctx, cancel := context.WithCancel(t.Context())
	gateway.remote.desktop.active = &desktopTarget{id: generation, mode: "local", ctx: ctx, cancel: cancel, session: session}
	t.Cleanup(gateway.remote.close)
	lock, err := openControllerLock(gateway.profile, os.O_CREATE|os.O_RDWR)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = lockController(lock); err != nil {
		t.Fatal(err)
	}
	defer unlockController(lock)
	server := httptest.NewServer(gateway.handler())
	defer server.Close()
	endpoint, _ = url.Parse(server.URL)
	port, _ := strconv.Atoi(endpoint.Port())
	gateway.origin = "http://localhost:" + endpoint.Port()
	if err = writePrivate(filepath.Join(gateway.profile, "control.json"), controlFile{Port: port, Token: gateway.token, PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(workspace, "chosen.bin")
	result, err := BrowserFileForConnection(gateway.configPath, generation, "save", item.ID, destination)
	if err != nil || result.Path != destination {
		t.Fatalf("native bridge save: %#v %v", result, err)
	}
	for _, path := range []string{destination, filepath.Join(workspace, "Downloads", item.Name)} {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, payload) {
			t.Errorf("save bytes mismatch: %v", err)
		}
	}
	receipt, err := readSavedDownload(s.profile, item.ID)
	if err != nil || receipt.Path != destination {
		t.Fatal("native Save As did not replace the saved location")
	}
	if _, err = BrowserFileForConnection(gateway.configPath, "old-connection", "save", item.ID, filepath.Join(workspace, "refused.bin")); err == nil {
		t.Fatal("old connection saved a download")
	}
}

// Runs the production native browser controller with actual Chromium. The
// browser download then crosses the production HTTP and chunked file paths.
func TestNativeDownloadActualChromiumToHost(t *testing.T) {
	if os.Getenv("CAGE_BROWSER_TEST_PLAYWRIGHT") == "" || os.Getenv("CAGE_BROWSER_TEST_CHROMIUM") == "" {
		t.Skip("set the test Playwright and Chromium paths")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	s, workspace, _ := nativeDownloadFixture(t, "macos", nil, nil)
	browserDirectory := filepath.Join(t.TempDir(), "native-browser")
	t.Cleanup(func() {
		client := s.windowsBrowserControl
		client.mu.Lock()
		defer client.mu.Unlock()
		if client.cmd != nil {
			// Give the test controller its stdin EOF so Playwright closes Chrome
			// before testing removes the browser's isolated profile directory.
			_ = client.input.Close()
			_ = client.cmd.Wait()
			client.cmd, client.input, client.output = nil, nil, nil
		}
	})
	s.windowsBrowserControl.start = func(ctx context.Context) (*exec.Cmd, error) {
		return exec.CommandContext(ctx, "node", filepath.Join(root, "scripts/guest-os/tests/native-download-host-fixture.mjs"), root, browserDirectory), nil
	}
	var item browserDownloadInfo
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		response := windowsBrowserRequestForTest(s, "GET", "/api/browser/tabs", nil)
		if response.Code != 200 {
			t.Fatalf("native tabs failed: %d %s", response.Code, response.Body.String())
		}
		var state struct{ Downloads []browserDownloadInfo }
		if err = json.Unmarshal(response.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		if len(state.Downloads) > 0 {
			item = state.Downloads[0]
			if item.State == "failed" {
				t.Fatal(item.Error)
			}
			if item.State == "saved" {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if item.State != "saved" || item.Path != filepath.Join(workspace, "Downloads", "native-download.bin") {
		t.Fatalf("not saved on the host: %#v", item)
	}
	payload := make([]byte, 131089)
	for index := range payload {
		payload[index] = byte(index % 251)
	}
	actual, err := os.ReadFile(item.Path)
	if err != nil || !bytes.Equal(actual, payload) {
		t.Fatalf("downloaded host bytes differ: %v", err)
	}
	response := windowsBrowserRequestForTest(s, "GET", "/api/browser/files/downloads/"+item.ID+"/content", nil)
	if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), payload) {
		t.Fatal("native HTTP content differs", response.Code)
	}
	t.Log("actual Chromium click downloaded 131089 bytes; native transfer published a verified host file and the production HTTP content returned identical bytes")
}

func TestNativeDownloadCompletionUpdatesIdleBrowserEvents(t *testing.T) {
	item := browserDownloadInfo{ID: uuid.NewString(), Name: "event.bin", State: "saved", Path: "Downloads/event.bin"}
	s, _, directory := nativeDownloadFixture(t, "macos", []browserDownloadInfo{item}, []byte("event payload"))
	block := filepath.Join(directory, "block")
	if err := os.WriteFile(block, nil, 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(s.handler())
	defer server.Close()
	endpoint, _ := url.Parse(server.URL)
	s.origin = "http://localhost:" + endpoint.Port()
	request, err := http.NewRequestWithContext(t.Context(), "GET", s.origin+"/api/browser/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(&http.Cookie{Name: s.cookieName(), Value: s.session})
	request.AddCookie(&http.Cookie{Name: s.cookieName() + "-desktop", Value: trustedViewProof(s.token)})
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	next := func() browserDownloadInfo {
		t.Helper()
		for scanner.Scan() {
			data, ok := strings.CutPrefix(scanner.Text(), "data: ")
			if !ok {
				continue
			}
			var state struct{ Downloads []browserDownloadInfo }
			if json.Unmarshal([]byte(data), &state) != nil || len(state.Downloads) != 1 {
				t.Fatal("invalid event snapshot")
			}
			return state.Downloads[0]
		}
		t.Fatalf("browser event ended before download state: %v", scanner.Err())
		return browserDownloadInfo{}
	}
	initial := next()
	if initial.State != "saving" || initial.Path != "" {
		t.Fatalf("incomplete copy published a host path: %#v", initial)
	}
	if err = os.Remove(block); err != nil {
		t.Fatal(err)
	}
	for result := next(); ; result = next() {
		if result.State == "saved" {
			got, err := os.ReadFile(result.Path)
			if err != nil || string(got) != "event payload" {
				t.Fatal("saved event preceded the host file")
			}
			break
		}
		if result.State == "failed" {
			t.Fatal(result.Error)
		}
	}
}
