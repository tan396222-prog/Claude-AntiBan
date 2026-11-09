//go:build linux

package reversesshfs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every spelling of ".git" that a casefold directory (ext4, f2fs, tmpfs with the feature) takes as
// the same entry is the same name for sameName. The directory is made under $CASEFOLD_DIR, or the
// temporary folder; the test is skipped where that is not a file system with the feature.
// To get one: mke2fs -t ext4 -O casefold -E encoding=utf8 image, mounted.
func TestSameNameCasefold(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	base := os.Getenv("CASEFOLD_DIR")
	if base == "" {
		base = t.TempDir()
	}
	dir := filepath.Join(base, "cf")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if out, err := exec.Command("chattr", "+F", dir).CombinedOutput(); err != nil {
		t.Skipf("no casefold directory here: %v: %s", err, out)
	}
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".GIT")); err != nil {
		t.Skip("the directory is case sensitive")
	}
	var found int
	for r := rune(0x80); r <= 0x10ffff; r++ {
		if r >= 0xd800 && r <= 0xdfff {
			continue
		}
		s := string(r)
		for _, name := range []string{".g" + s + "it", ".gi" + s + "t", ".git" + s, s + ".git", s + "git", ".g" + s + "t", ".gi" + s, "." + s + "it"} {
			if strings.Contains(name, "/") {
				continue
			}
			if _, err := os.Lstat(filepath.Join(dir, name)); err != nil {
				continue
			}
			found++
			if !sameName(name, ".git") {
				t.Errorf("%+q is .git on this file system, and sameName says it is not", name)
			}
		}
	}
	if found == 0 {
		t.Error("no spelling found: the test does not reach the file system")
	}
}
