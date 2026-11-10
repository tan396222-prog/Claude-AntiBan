package workbench

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sylvinus/agent-vm/internal/guestos"
)

func TestMacOSClipboardUsesBoundGUIAgent(t *testing.T) {
	s := testServer(t)
	s.macosGuest = &guestos.MacOSProfile{Instance: "mac", User: "guestdev"}
	if err := os.MkdirAll(s.instanceDir(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.instanceDir(), "ssh.config"), []byte("Host lima-mac\n Port 54432\n User root\n"), 0600); err != nil {
		t.Fatal(err)
	}
	selected := []string{"/tmp/中文 ' $(touch unexpected).txt"}
	for _, operation := range []string{"stream", "import", "export", "request"} {
		cmd, source, err := s.clipboardCommand(context.Background(), operation, clipboardFileSelection{Paths: selected, User: "guest"})
		if err != nil || cmd.Err != nil || source != "guest" {
			t.Fatalf("%s transport: %v", operation, err)
		}
		command := cmd.Args[len(cmd.Args)-1]
		if command != "exec '/Applications/Cage Guest Clipboard.app/Contents/MacOS/CageGuestClipboard' --client "+operation ||
			!slices.Contains(cmd.Args, "guestdev") || slices.Contains(cmd.Args, "root") || strings.Contains(command, selected[0]) {
			t.Fatalf("%s escaped its fixed guest transport", operation)
		}
		if operation == "export" {
			data, _ := io.ReadAll(cmd.Stdin)
			var actual []string
			if json.Unmarshal(data, &actual) != nil || !slices.Equal(actual, selected) {
				t.Fatal("file selections did not travel as stdin JSON")
			}
		}
	}
	for _, item := range []struct{ operation, user string }{{"execute", "guest"}, {"export", "root"}, {"export", "cage-browser"}} {
		if _, _, err := s.clipboardCommand(context.Background(), item.operation, clipboardFileSelection{User: item.user}); err == nil {
			t.Fatalf("accepted %s for %s", item.operation, item.user)
		}
	}
}
