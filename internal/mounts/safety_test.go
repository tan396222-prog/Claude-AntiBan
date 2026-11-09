package mounts

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sylvinus/agent-vm/internal/paths"
)

// What Lima cannot mount, or 0.2 refused: said why.
func TestUnmountable(t *testing.T) {
	for dir, want := range map[string]string{
		"/home/me/proj":     "",
		"/home/me/my proj":  "whitespace",
		"/home/me/a\tb":     "whitespace",
		`/home/me/a"b`:      "a quote",
		`/home/me/a\b`:      "a backslash",
		"/home/me/a\x01b":   "a control character",
		"/home/me/été/proj": "",
		"/home/me/R&D":      "",
		"/{{.Home}}":        "a template",
		"/home/me/a\xffb":   "not valid UTF-8",
		"/home/me/a\u2028b": "whitespace",
	} {
		why, bad := Unmountable(dir)
		if bad != (want != "") || !strings.Contains(why, want) {
			t.Errorf("%q: %q %v", dir, why, bad)
		}
	}
}

// A single file given as a symlink (stow's dotfiles) is staged as the file
// it names, in step with it: not as a link pointing out of the staging
// folder, which the VM could not follow.
func TestStageSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "dotfiles", "gitconfig")
	os.MkdirAll(filepath.Dir(real), 0o755)
	os.WriteFile(real, []byte("v1"), 0o644)
	link := filepath.Join(dir, ".gitconfig")
	os.Symlink(real, link)
	dst := filepath.Join(dir, "stage", "f")
	if !Stage(link, dst, io.Discard) {
		t.Fatal("not staged")
	}
	if fi, err := os.Lstat(dst); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("staged as a link: %v %v", fi, err)
	}
	os.WriteFile(real, []byte("v2"), 0o644)
	if b, _ := os.ReadFile(dst); string(b) != "v2" {
		t.Errorf("not in step with the file: %q", b)
	}
}

// Where the file system ignores case, the home folder in other capitals is
// the home folder.
func TestUnsafeLocationNoCase(t *testing.T) {
	s := newSandbox(t)
	refs := Refs(s.home, filepath.Join(s.root, "self"), s.state, filepath.Join(s.state, "lima"))
	upper := strings.ToUpper(s.home)
	// On a file system that ignores case the uppercased home is the
	// home folder, and refused below; here case matters.
	if !paths.NoCase() {
		if _, bad := UnsafeLocation(upper, refs); bad {
			t.Error("refused where case matters")
		}
	}
	old := paths.NoCase
	paths.NoCase = func() bool { return true }
	t.Cleanup(func() { paths.NoCase = old })
	refs = Refs(s.home, filepath.Join(s.root, "self"), s.state, filepath.Join(s.state, "lima"))
	if why, bad := UnsafeLocation(upper, refs); !bad || !strings.Contains(why, "your home directory") {
		t.Errorf("home in capitals: %q %v", why, bad)
	}
	if why, bad := UnsafeLocation(strings.ToUpper(s.state)+"/x", refs); !bad {
		t.Errorf("inside the state dir in capitals: %q %v", why, bad)
	}
}

// Folders of credentials and applications' data under the home folder are
// refused as projects, from inside, equal or above, through links, with the
// case a case-ignoring file system gives them; the other folders of the home
// folder are not.
func TestCredentialLocation(t *testing.T) {
	s := newSandbox(t)
	for _, d := range []string{".ssh/keys", ".config/gcloud", "Library/Keychains", ".local/share/keyrings", ".local/src", ".sshx", "work/.ssh-notes", ".mozilla"} {
		os.MkdirAll(filepath.Join(s.home, d), 0o755)
	}
	os.MkdirAll(filepath.Join(s.root, "appdata", "app"), 0o755)
	t.Setenv("APPDATA", filepath.Join(s.root, "appdata"))
	t.Setenv("LOCALAPPDATA", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	os.Symlink(s.home+"/.ssh", s.proj+"/lnk")
	refs := s.refs()
	for _, c := range []struct {
		dir string
		bad bool
	}{
		{s.home + "/.ssh", true},
		{s.home + "/.ssh/keys", true},
		{s.home + "/.config", true},
		{s.home + "/.config/gcloud", true},
		{s.home + "/Library/Keychains", true},
		{s.home + "/.mozilla", true},
		{s.home + "/.local", true},
		{s.home + "/.local/share", true},
		{s.home + "/.local/share/keyrings", true},
		{s.proj + "/lnk", true},
		{s.root + "/appdata/app", true},
		{s.proj, false},
		{s.proj + "/sub", false},
		{s.home + "/.local/src", false},
		{s.home + "/.sshx", false},
		{s.home + "/work/.ssh-notes", false},
		{s.root + "/data/a", false},
	} {
		why, bad := CredentialLocation(c.dir, refs)
		if bad != c.bad {
			t.Errorf("%s: %q %v", c.dir, why, bad)
		}
		if _, bad := UnsafeProject(c.dir, refs); c.bad && !bad {
			t.Errorf("UnsafeProject lets %s through", c.dir)
		}
	}
	if why, _ := CredentialLocation(s.home+"/.ssh", refs); !strings.Contains(why, "~/.ssh") {
		t.Errorf("why: %q", why)
	}
	// A credentials folder that is a link to another: that one counts too.
	os.RemoveAll(s.home + "/.aws")
	os.MkdirAll(s.root+"/dotfiles/aws", 0o755)
	os.Symlink(s.root+"/dotfiles/aws", s.home+"/.aws")
	if _, bad := CredentialLocation(s.root+"/dotfiles/aws", refs); !bad {
		t.Error("the target of ~/.aws was let through")
	}
	// Other capitals, where the file system ignores case.
	old := paths.NoCase
	paths.NoCase = func() bool { return true }
	t.Cleanup(func() { paths.NoCase = old })
	refs = s.refs()
	if _, bad := CredentialLocation(strings.ToUpper(s.home)+"/.SSH", refs); !bad {
		t.Error("~/.SSH let through where case is ignored")
	}
}
