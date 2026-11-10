//go:build linux || darwin || windows

package reversesshfs

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/pkg/sftp"
)

func cloudClient(t *testing.T, directory string, ro bool) (*CloudShare, *sftp.Client) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := OpenCloudShare(dir, ro)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil || !grant.MatchesRoot(info) {
		t.Fatal("grant does not match selected directory", err)
	}
	a, b := net.Pipe()
	t.Cleanup(func() { b.Close(); grant.Close() })
	go grant.Serve(a)
	c, err := sftp.NewClientPipe(b, b)
	if err != nil {
		t.Fatal(err)
	}
	return grant, c
}

func TestCloudProjectLifecycle(t *testing.T) {
	root := t.TempDir()
	hostFile := filepath.Join(root, "source.go")
	if err := os.WriteFile(hostFile, []byte("before"), 0644); err != nil {
		t.Fatal(err)
	}
	grant, c := cloudClient(t, root, false)
	f, err := c.Open("/source.go")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(f)
	if err != nil || string(data) != "before" {
		t.Fatalf("remote read = %q, %v", data, err)
	}
	f.Close()
	if err = c.Mkdir("/src"); err != nil {
		t.Fatal(err)
	}
	tmp, err := c.Create("/src/save")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tmp.Write([]byte("after")); err != nil {
		t.Fatal(err)
	}
	if err = tmp.Close(); err != nil {
		t.Fatal(err)
	}
	if err = c.PosixRename("/src/save", "/source.go"); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(hostFile)
	if err != nil || string(data) != "after" {
		t.Fatalf("local read = %q, %v", data, err)
	}
	open, err := c.OpenFile("/source.go", os.O_WRONLY)
	if err != nil {
		t.Fatal(err)
	}
	if err = grant.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = open.Write([]byte("revoked")); err == nil {
		t.Fatal("revoked open handle wrote")
	}
	if _, err = c.Stat("/source.go"); err == nil {
		t.Fatal("revoked grant usable")
	}
}

func TestCloudGitMetadataFollowsGrantPermissions(t *testing.T) {
	for _, readonly := range []bool{true, false} {
		t.Run(map[bool]string{true: "read-only", false: "read-write"}[readonly], func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, ".git", "refs", "heads"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0600); err != nil {
				t.Fatal(err)
			}
			for _, dir := range []string{".vscode", ".claude", ".codex"} {
				if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
					t.Fatal(err)
				}
			}
			_, c := cloudClient(t, root, readonly)
			files, err := c.ReadDir("/")
			if err != nil || !slices.ContainsFunc(files, func(f os.FileInfo) bool { return f.Name() == ".git" }) {
				t.Fatalf("Git directory hidden: %v", err)
			}
			f, err := c.Open("/.git/HEAD")
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(f)
			f.Close()
			if err != nil || string(data) != "ref: refs/heads/main\n" {
				t.Fatalf("Git HEAD unreadable: %q %v", data, err)
			}
			for _, name := range []string{".git/index", ".git/refs/heads/main", ".git/config", ".gitmodules", ".gitconfig", ".lfsconfig", ".pre-commit-config.yaml", ".git-credentials", ".env", ".npmrc", "private.key", ".vscode/settings.json", ".claude/settings.json", ".codex/config.toml"} {
				f, err = c.Create("/" + name + ".lock")
				if readonly {
					if err == nil {
						f.Close()
						t.Fatal("Git write allowed by read-only grant")
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.Write([]byte("git-write"))
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
				if err = c.PosixRename("/"+name+".lock", "/"+name); err != nil {
					t.Fatal(err)
				}
				if err = c.Remove("/" + name); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestCloudHostileClient(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "project")
	if err := os.MkdirAll(filepath.Join(root, ".Git"), 0700); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(tmp, "outside")
	for _, name := range []string{out, filepath.Join(root, ".eNv.production"), filepath.Join(root, ".Git", "config")} {
		if err := os.WriteFile(name, []byte("private"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	hard := filepath.Join(root, "alias")
	if err := os.Link(out, hard); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(out, filepath.Join(root, "symlink")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(".Git", filepath.Join(root, "inner")); err != nil {
			t.Fatal(err)
		}
	}
	_, c := cloudClient(t, root, false)
	for _, p := range []string{"../outside", "/alias", "/symlink", "/inner/config"} {
		if _, err := c.Open(p); err == nil {
			t.Fatalf("opened %s", p)
		}
	}
	for _, p := range []string{"/alias"} {
		if _, err := c.Create(p); err == nil {
			t.Fatalf("created/truncated %s", p)
		}
		if err := c.Truncate(p, 0); err == nil {
			t.Fatalf("truncate %s", p)
		}
	}
	if err := c.Link("/alias", "/link"); err == nil {
		t.Fatal("hard link created")
	}
	if err := c.Symlink("../outside", "/link"); err == nil {
		t.Fatal("symlink created")
	}
	if err := c.PosixRename("/alias", "/.env"); err != nil {
		t.Fatal("project entry rename was blocked by its filename", err)
	}
	files, err := c.ReadDir("/")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(files, func(fi os.FileInfo) bool { return fi.Name() == ".eNv.production" }) {
		t.Fatal("project dotfile was hidden")
	}
	content, err := os.ReadFile(out)
	if err != nil || string(content) != "private" {
		t.Fatal("outside data changed", err)
	}
}

func TestCloudReadOnlyAndRootReplacement(t *testing.T) {
	original := t.TempDir()
	if err := os.WriteFile(filepath.Join(original, "allowed"), []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	g, c := cloudClient(t, original, true)
	if _, err := c.Create("/new"); err == nil {
		t.Fatal("created on read-only grant")
	}
	if err := c.Remove("/allowed"); err == nil {
		t.Fatal("removed on read-only grant")
	}
	if err := c.Mkdir("/dir"); err == nil {
		t.Fatal("mkdir on read-only grant")
	}
	other, err := os.Stat(t.TempDir())
	if err != nil || g.MatchesRoot(other) {
		t.Fatal("root identity accepted replacement", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Rename(original, original+"-old"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(original + "-old") })
		if err := os.Mkdir(original, 0700); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Stat("/allowed"); err != nil {
			t.Fatal("lost pinned root", err)
		}
		if err := os.WriteFile(filepath.Join(original, "replacement"), []byte("private"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Open("/replacement"); err == nil {
			t.Fatal("followed replacement root")
		}
	}
}

func TestCloudRefusesReusingRevokedGrant(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g, err := OpenCloudShare(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	a, b := net.Pipe()
	defer b.Close()
	if !errors.Is(g.Serve(a), os.ErrClosed) {
		t.Fatal("accepted revoked grant")
	}
}

func TestCloudOpenFilesBoundAcrossShares(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	grants := make([]*CloudShare, 0, 3)
	clients := make([]*sftp.Client, 0, 3)
	for range 3 {
		g, c := cloudClient(t, root, true)
		grants = append(grants, g)
		clients = append(clients, c)
	}
	for _, client := range clients[:2] {
		for range 64 {
			if _, err := client.Open("/f"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := client.Open("/f"); err == nil {
			t.Fatal("per-stream handle cap bypassed")
		}
	}
	if _, err := clients[2].Open("/f"); err == nil {
		t.Fatal("process-wide handle cap bypassed")
	}
	if err := grants[0].Close(); err != nil {
		t.Fatal(err)
	}
	f, err := clients[2].Open("/f")
	if err != nil {
		t.Fatal("revocation leaked file handles", err)
	}
	f.Close()
}
