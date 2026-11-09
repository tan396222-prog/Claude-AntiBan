package workbench

import (
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sylvinus/agent-vm/internal/mounts"
)

// NTFS junctions are available to ordinary users. Exercise the same private
// directory through an unrelated DOS path in both sharing entry points.
func TestWindowsJunctionCannotExposePrivateDirectory(t *testing.T) {
	s := testServer(t)
	parent := t.TempDir()
	alias := filepath.Join(parent, "环境别名")
	cmd := exec.Command("cmd.exe", "/d", "/v:off", "/c", "mklink", "/J", alias, s.profile)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create directory junction: %v, %s", err, out)
	}
	if err := os.Mkdir(filepath.Join(s.profile, "stored-data"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{alias, filepath.Join(alias, "stored-data")} {
		if _, err := validateWorkspace(dir, s.profile); err == nil {
			t.Fatal("private alias accepted as a workbench project", dir)
		}
		if _, denied := mounts.UnsafeProject(dir, mounts.Refs("", "", s.profile)); !denied {
			t.Fatal("private alias accepted as a CLI share", dir)
		}
		if w := request(s, "GET", "/api/projects/directories?path="+url.QueryEscape(dir), "", "", true); w.Code != 403 {
			t.Fatal("private alias browsable", w.Code)
		}
	}
	visible := filepath.Join(parent, "public")
	if err := os.Mkdir(visible, 0700); err != nil {
		t.Fatal(err)
	}
	w := request(s, "GET", "/api/projects/directories?path="+url.QueryEscape(parent), "", "", true)
	var listing struct{ Directories []struct{ Name string } }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &listing) != nil || len(listing.Directories) != 1 || listing.Directories[0].Name != "public" {
		t.Fatal("directory picker exposed the private alias or hid the safe sibling", w.Code, w.Body.String())
	}
}
