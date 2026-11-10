package workbench

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/guestos"
)

func TestMacOSAttachmentPathRejectsOtherUsersAndEscapes(t *testing.T) {
	s := &Server{macosGuest: &guestos.MacOSProfile{User: "cage"}}
	root := "/Users/cage/.local/share/cage/attachments/files/" + uuid.NewString() + "/" + uuid.NewString() + "/"
	if !s.validMacOSAttachmentPath(root+"附件.txt", "附件.txt") {
		t.Fatal("bound native attachment rejected")
	}
	for _, value := range []string{strings.Replace(root, "/cage/", "/another/", 1) + "附件.txt", root + "../附件.txt", root + "other.txt", "/Users/cage/.ssh/id_ed25519"} {
		if s.validMacOSAttachmentPath(value, "附件.txt") {
			t.Fatalf("unbound attachment accepted: %q", value)
		}
	}
}

func TestMacOSTerminalUsesSharedAttachmentReceiver(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shared macOS/Linux guest receiver requires POSIX UnixStreamServer")
	}
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv is required for the shared Python receiver")
	}
	home := t.TempDir()
	contents := []byte("native attachment 中文\x00\x01")
	var previous string
	for attempt := 0; attempt < 2; attempt++ {
		cmd := exec.Command("uv", "run", "--no-project", "python", "-I", "-c", cage.DesktopFileReceiver(), "--save-file", "file.bin")
		cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home)
		cmd.Stdin = bytes.NewReader(contents)
		output, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		var saved struct {
			OK   bool   `json:"ok"`
			Path string `json:"path"`
		}
		if json.Unmarshal(output, &saved) != nil || !saved.OK || !strings.HasPrefix(saved.Path, filepath.Join(home, ".local", "share", "cage", "attachments", "files")+string(os.PathSeparator)) {
			t.Fatalf("receiver returned invalid attachment: %s", output)
		}
		actual, err := os.ReadFile(saved.Path)
		if err != nil || !bytes.Equal(actual, contents) || saved.Path == previous {
			t.Fatal("attachment bytes changed or duplicate name replaced a prior attachment")
		}
		previous = saved.Path
	}
}
