package workbench

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/guestos"
)

func init() {
	if os.Getenv("CAGE_TEST_WINDOWS_ATTACHMENT_RELAY") != "1" {
		return
	}
	if len(os.Args) != 4 || os.Args[1] != "--relay-profile" || os.Args[3] != "attachments" {
		os.Exit(2)
	}
	// Read the same JSON line and framed bytes as the real native endpoint.
	var request map[string]string
	line := make([]byte, 0, 4096)
	for {
		var b [1]byte
		if _, err := io.ReadFull(os.Stdin, b[:]); err != nil {
			os.Exit(2)
		}
		if b[0] == '\n' {
			break
		}
		line = append(line, b[0])
		if len(line) > 4096 {
			os.Exit(2)
		}
	}
	if json.Unmarshal(line, &request) != nil || request["action"] != "save" {
		os.Exit(2)
	}
	var size int64
	for {
		var header [4]byte
		if _, err := io.ReadFull(os.Stdin, header[:]); err != nil {
			os.Exit(2)
		}
		n := binary.LittleEndian.Uint32(header[:])
		if n == 0 {
			break
		}
		if n > 65536 {
			os.Exit(2)
		}
		if _, err := io.CopyN(io.Discard, os.Stdin, int64(n)); err != nil {
			os.Exit(2)
		}
		size += int64(n)
	}
	path := `C:\Users\Guest\Cage\Attachments\0123456789abcdef0123456789abcdef\` + request["name"]
	if os.Getenv("CAGE_TEST_WINDOWS_ATTACHMENT_BAD_PATH") == "1" {
		path = `C:\outside\` + request["name"]
	}
	reply := map[string]any{"ok": true, "scope": "attachments", "path": path}
	if os.Getenv("CAGE_TEST_WINDOWS_ATTACHMENT_NO_SIZE") != "1" {
		reply["size"] = size
	}
	_ = json.NewEncoder(os.Stdout).Encode(reply)
	os.Exit(0)
}

func TestWindowsDesktopUploadHandlerKeepsPathsGuestPrivate(t *testing.T) {
	s := testServer(t)
	binding := guestos.WindowsProfile{Version: 1, VMID: uuid.New(), GUIUserSID: "S-1-5-21-1-2-3-1001", GUIUser: "Guest", InstanceDirectory: filepath.Join(s.profile, "unused-vm"), PairingFile: filepath.Join(s.profile, "unused-pair"), RelayExecutable: os.Args[0]}
	if err := guestos.BindWindowsProfile(s.profile, binding); err != nil {
		t.Fatal(err)
	}
	s.windowsGuest = &binding
	s.ready = true
	t.Setenv("CAGE_TEST_WINDOWS_ATTACHMENT_RELAY", "1")
	id := uuid.NewString()
	for _, sizeKnown := range []bool{true, false} {
		if !sizeKnown {
			t.Setenv("CAGE_TEST_WINDOWS_ATTACHMENT_NO_SIZE", "1")
		}
		w := windowsBrowserRequestForTest(s, http.MethodPost, "/api/input/files/desktop/"+id+"?name=note.txt", []byte("abc"))
		var response map[string]any
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil || response["ok"] != true || response["id"] != id || response["name"] != "note.txt" {
			t.Fatalf("real desktop upload handler failed: %d %s", w.Code, w.Body.String())
		}
		if response["path"] != nil || strings.Contains(w.Body.String(), "Attachments") {
			t.Fatal("desktop upload exposed its native staging path")
		}
		_, hasSize := response["size"]
		if hasSize != sizeKnown {
			t.Fatal("desktop response invented or lost a native byte count")
		}
		if hasSize && response["size"] != float64(3) {
			t.Fatal("desktop response changed actual received bytes")
		}
	}
	result, err := s.windowsSaveAttachment(t.Context(), "note.txt", strings.NewReader("abc"))
	if err != nil || !validWindowsAttachmentPath(result["path"].(string), "note.txt") {
		t.Fatal("terminal/image native save lost its validated path")
	}
	t.Setenv("CAGE_TEST_WINDOWS_ATTACHMENT_BAD_PATH", "1")
	if w := windowsBrowserRequestForTest(s, http.MethodPost, "/api/input/files/desktop/"+uuid.NewString()+"?name=note.txt", []byte("abc")); w.Code == http.StatusOK {
		t.Fatal("path was hidden before validating native receipt")
	}
}
