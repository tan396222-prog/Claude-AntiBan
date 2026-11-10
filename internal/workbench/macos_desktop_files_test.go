package workbench

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestMacOSDesktopAttachmentsUsePrivateNativeOperations(t *testing.T) {
	const id = "aa447f34-18bc-48ba-b3f8-93bbf7f7a315"
	var mu sync.Mutex
	var uploaded, pasted []byte
	var calls []string
	var rejectAdd, discarded bool
	guest := newMacWorkspaceGuest(t, func(channel ssh.Channel, command string) (uint32, bool) {
		reader := bufio.NewReader(channel)
		if command == "exec '/Applications/Cage Guest Clipboard.app/Contents/MacOS/CageGuestClipboard' --client request" {
			var request struct {
				Operation string
				Files     []string
			}
			if json.NewDecoder(reader).Decode(&request) != nil || request.Operation != "discard" || len(request.Files) != 1 || request.Files[0] != "/tmp/cage-clipboard-12345/资料.bin" {
				return 1, true
			}
			mu.Lock()
			discarded = true
			mu.Unlock()
			json.NewEncoder(channel).Encode(map[string]any{"ok": true})
			return 0, true
		}
		if command == "exec '/Applications/Cage Guest Clipboard.app/Contents/MacOS/CageGuestClipboard' --client upload" {
			line, err := reader.ReadBytes('\n')
			var request struct{ Name string }
			if err != nil || json.Unmarshal(line, &request) != nil || (request.Name != "资料.bin" && request.Name != "image.png") {
				return 1, true
			}
			var data bytes.Buffer
			for {
				var header [4]byte
				if _, err := io.ReadFull(reader, header[:]); err != nil {
					return 1, true
				}
				length := binary.LittleEndian.Uint32(header[:])
				if length == 0 {
					break
				}
				if length > 65536 {
					return 1, true
				}
				if _, err := io.CopyN(&data, reader, int64(length)); err != nil {
					return 1, true
				}
			}
			mu.Lock()
			if request.Name == "image.png" {
				pasted = append([]byte(nil), data.Bytes()...)
			} else {
				uploaded = append([]byte(nil), data.Bytes()...)
			}
			mu.Unlock()
			json.NewEncoder(channel).Encode(map[string]any{"paths": []string{"/tmp/cage-clipboard-12345/" + request.Name}})
			return 0, true
		}
		if !strings.Contains(command, `dev.cage.GuestClaudeWindow/window.sock`) {
			return 1, false
		}
		line, err := reader.ReadBytes('\n')
		var request struct {
			Action, ID, Kind string
			Files            []string
			Length           int
			PID, Window      int
		}
		if err != nil || json.Unmarshal(line, &request) != nil {
			return 1, true
		}
		mu.Lock()
		calls = append(calls, request.Action)
		mu.Unlock()
		switch request.Action {
		case "files-start":
			if (request.Kind != "paste" && request.Kind != "image") || request.PID != 100 || request.Window != 7 {
				return 1, true
			}
			json.NewEncoder(channel).Encode(map[string]any{"ok": true, "id": id})
		case "files-validate", "files-finish", "files-finish-image", "files-cancel":
			if request.ID != id {
				return 1, true
			}
			json.NewEncoder(channel).Encode(map[string]any{"ok": true})
		case "files-add":
			if request.ID != id || len(request.Files) != 1 || (request.Files[0] != "/tmp/cage-clipboard-12345/资料.bin" && request.Files[0] != "/tmp/cage-clipboard-12345/image.png") {
				return 1, true
			}
			mu.Lock()
			reject := rejectAdd
			mu.Unlock()
			if reject {
				json.NewEncoder(channel).Encode(map[string]any{"error": "attachment_cancelled"})
				return 0, true
			}
			json.NewEncoder(channel).Encode(map[string]any{"ok": true, "id": id})
		case "files-poll":
			json.NewEncoder(channel).Encode(map[string]any{"available": true, "uploads": []any{map[string]any{"id": id, "kind": "chooser", "multiple": true, "target": "desktop"}}, "downloads": []any{}})
		case "paste-image":
			if request.Length <= 0 || request.Length > maxImageBytes || request.PID != 100 || request.Window != 7 {
				return 1, true
			}
			data := make([]byte, request.Length)
			if _, err := io.ReadFull(reader, data); err != nil {
				return 1, true
			}
			mu.Lock()
			pasted = data
			mu.Unlock()
			json.NewEncoder(channel).Encode(map[string]any{"ok": true})
		default:
			return 1, true
		}
		return 0, true
	})
	s := guest.s
	missingTarget := windowsBrowserRequestForTest(s, http.MethodPost, "/api/input/files/start", []byte(`{"target":"desktop","kind":"paste"}`))
	if missingTarget.Code == http.StatusOK {
		t.Fatal("attachment without an observed window was accepted")
	}
	start := windowsBrowserRequestForTest(s, http.MethodPost, "/api/input/files/start", []byte(`{"target":"desktop","kind":"paste","pid":100,"window":7}`))
	if start.Code != http.StatusOK || !strings.Contains(start.Body.String(), id) {
		t.Fatalf("start failed: %d %s", start.Code, start.Body.String())
	}
	content := []byte{0, 255, 128, 1, 2, 3}
	upload := windowsBrowserRequestForTest(s, http.MethodPost, "/api/input/files/desktop/"+id+"?name=%E8%B5%84%E6%96%99.bin", content)
	if upload.Code != http.StatusOK || strings.Contains(upload.Body.String(), "/tmp/") {
		t.Fatalf("upload failed or exposed private path: %d %s", upload.Code, upload.Body.String())
	}
	for _, action := range []string{"finish", "cancel"} {
		response := windowsBrowserRequestForTest(s, http.MethodPost, "/api/input/files/"+action, []byte(`{"target":"desktop","id":"`+id+`"}`))
		if response.Code != http.StatusOK {
			t.Fatalf("%s failed: %s", action, response.Body.String())
		}
	}
	poll := windowsBrowserRequestForTest(s, http.MethodGet, "/api/input/files/desktop/requests", nil)
	if poll.Code != http.StatusOK || !strings.Contains(poll.Body.String(), `"kind":"chooser"`) {
		t.Fatalf("chooser poll failed: %d %s", poll.Code, poll.Body.String())
	}
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	prepared := windowsBrowserRequestForTest(s, http.MethodPost, "/api/input/paste/start", []byte(`{"target":"desktop","pid":100,"window":7}`))
	if prepared.Code != http.StatusOK || !strings.Contains(prepared.Body.String(), id) {
		t.Fatalf("image preparation failed: %d %s", prepared.Code, prepared.Body.String())
	}
	body, _ := json.Marshal(map[string]any{"target": "desktop", "png": base64.StdEncoding.EncodeToString(picture.Bytes()), "transfer": id, "pid": 100, "window": 7})
	response := windowsBrowserRequestForTest(s, http.MethodPost, "/api/input/image", body)
	if response.Code != http.StatusOK {
		t.Fatalf("image failed: %d %s", response.Code, response.Body.String())
	}
	mu.Lock()
	if !bytes.Equal(uploaded, content) || !bytes.Equal(pasted, picture.Bytes()) {
		mu.Unlock()
		t.Fatal("private transports changed file or PNG bytes")
	}
	if strings.Join(calls, ",") != "files-start,files-validate,files-add,files-finish,files-cancel,files-poll,files-start,files-validate,files-add,files-finish-image" {
		mu.Unlock()
		t.Fatalf("native operations diverged: %v", calls)
	}
	rejectAdd = true
	mu.Unlock()
	failed := windowsBrowserRequestForTest(s, http.MethodPost, "/api/input/files/desktop/"+id+"?name=%E8%B5%84%E6%96%99.bin", content)
	mu.Lock()
	defer mu.Unlock()
	if failed.Code == http.StatusOK || !discarded {
		t.Fatal("a completed upload abandoned by its window was not cleaned")
	}
}

func TestMacOSDesktopAttachmentPathsRejectEscapes(t *testing.T) {
	if !validMacOSClipboardAttachment("/var/folders/ab/xyz/T/cage-clipboard-123/资料.bin", "资料.bin") {
		t.Fatal("private guest cache refused")
	}
	for _, value := range []string{"/Users/cage/secret.txt", "/tmp/cage-clipboard-123/../secret.txt", "/tmp/cage-clipboard-123/file:stream", "/tmp/other-123/资料.bin", "/tmp/cage-clipboard-abc/资料.bin"} {
		if validMacOSClipboardAttachment(value, "资料.bin") {
			t.Fatal("unregistered path form accepted")
		}
	}
}
