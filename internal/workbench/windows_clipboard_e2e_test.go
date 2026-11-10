package workbench

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sylvinus/agent-vm/internal/clipboardfiles"
	"github.com/sylvinus/agent-vm/internal/guestos"
)

// Opt-in: copies only synthetic QA clipboard data in an already unlocked VM.
// The ordinary GUI relay, HTTP authorization, opaque selections and file bytes
// all use the shipped workbench handlers.
func TestWindowsGuestClipboardWorkbench(t *testing.T) {
	profile := os.Getenv("CAGE_WINDOWS_GUEST_QA_PROFILE")
	if runtime.GOOS != "windows" || profile == "" {
		t.Skip("isolated Windows GUI guest not configured")
	}
	binding, err := guestos.LoadWindowsProfile(profile)
	if err != nil || binding == nil {
		t.Fatalf("QA Windows profile unavailable: %v", err)
	}
	s := testServer(t)
	if err := guestos.BindWindowsProfile(s.profile, *binding); err != nil {
		t.Fatal(err)
	}
	s.windowsGuest = binding
	s.windowsState.Running = true
	s.ready = true
	server := httptest.NewServer(s.handler())
	defer server.Close()
	s.origin = server.URL
	headers := http.Header{"Origin": {s.origin}, "Cookie": {s.cookieName() + "=" + s.session + "; " + s.cookieName() + "-desktop=" + trustedViewProof(s.token)}}
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(s.origin, "http")+"/api/clipboard", headers)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.SetReadDeadline(time.Now().Add(30 * time.Second))
	var hello clipboardPayload
	if err = ws.ReadJSON(&hello); err != nil || hello.Hello == nil || hello.Hello.OS != "windows" || !hello.Hello.Available {
		t.Fatalf("public GUI hello missing: %v", err)
	}
	send := func(payload map[string]any, serial int) {
		t.Helper()
		payload["serial"] = serial
		if err := ws.WriteJSON(payload); err != nil {
			t.Fatal(err)
		}
		for {
			var event clipboardPayload
			if err := ws.ReadJSON(&event); err != nil {
				t.Fatal(err)
			}
			if event.Error != "" {
				t.Fatal("guest rejected QA clipboard write")
			}
			if event.Ack == serial {
				return
			}
		}
	}
	send(map[string]any{"text": "Cage workbench 中文🙂\nsecond line"}, 1)
	img := image.NewRGBA(image.Rect(0, 0, 12, 9))
	img.Set(0, 0, color.RGBA{R: 60, G: 110, B: 170, A: 255})
	var pngData bytes.Buffer
	if err = png.Encode(&pngData, img); err != nil {
		t.Fatal(err)
	}
	send(map[string]any{"png": base64.StdEncoding.EncodeToString(pngData.Bytes())}, 2)
	root := t.TempDir()
	folder := filepath.Join(root, "复制目录")
	if err = os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	content := []byte{0, 255, 128, 1, 2, 3}
	if err = os.WriteFile(filepath.Join(folder, "内容.bin"), content, 0600); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err = writeClipboardArchive(&archive, []string{folder}); err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest("POST", s.origin+"/api/clipboard/files", &archive)
	request.Header = headers.Clone()
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var uploaded struct {
		Files string `json:"files"`
	}
	err = json.NewDecoder(response.Body).Decode(&uploaded)
	response.Body.Close()
	if response.StatusCode != 200 || err != nil || !browserFileID.MatchString(uploaded.Files) {
		t.Fatalf("public file import: %d %v", response.StatusCode, err)
	}
	send(map[string]any{"files": uploaded.Files}, 3)
	request, _ = http.NewRequest("GET", s.origin+"/api/clipboard/files/"+uploaded.Files, nil)
	request.Header = headers.Clone()
	response, err = server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("public file export failed")
	}
	cache := clipboardfiles.CacheStore{Directory: filepath.Join(t.TempDir(), "clipboard-receipts")}
	selection, err := receiveClipboardArchive(response, cache)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Release(selection.ID)
	paths := selection.Paths
	actual, err := os.ReadFile(filepath.Join(paths[0], "内容.bin"))
	if err != nil || !bytes.Equal(actual, content) {
		t.Fatal("public file bytes changed")
	}
	// The real process exit trailer is required even after tar EOF.
	if response.Trailer.Get("X-Cage-Clipboard-Status") != "ok" {
		t.Fatal("missing producer completion")
	}
	_, _ = io.Copy(io.Discard, response.Body)
	fmt.Println("Windows workbench clipboard text/PNG/file stream passed")
}
