//go:build darwin || linux

package reversesshfs

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// Even when the client races normal file edits, neither the read nor the
// write path may follow the replacement symlink (including on truncation).
func TestCloudLinkSwapAndFifo(t *testing.T) {
	tmp := t.TempDir()
	outside, root := filepath.Join(tmp, "outside"), filepath.Join(tmp, "working")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(root, "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	_, c := cloudClient(t, root, false)
	if _, err := c.Open("/fifo"); err == nil {
		t.Fatal("read FIFO opened")
	}
	if _, err := c.OpenFile("/fifo", os.O_WRONLY); err == nil {
		t.Fatal("write FIFO opened")
	}

	swapped := filepath.Join(root, "swapped")
	done := make(chan struct{})
	raceErr := make(chan error, 1)
	go func() {
		for {
			select {
			case <-done:
				raceErr <- nil
				return
			default:
			}
			link, regular := filepath.Join(root, "link-temp"), filepath.Join(root, "regular-temp")
			if err := os.Symlink(outside, link); err != nil {
				raceErr <- err
				return
			}
			if err := os.Rename(link, swapped); err != nil {
				raceErr <- err
				return
			}
			if err := os.WriteFile(regular, []byte("public"), 0600); err != nil {
				raceErr <- err
				return
			}
			if err := os.Rename(regular, swapped); err != nil {
				raceErr <- err
				return
			}
		}
	}()
	defer func() {
		close(done)
		if err := <-raceErr; err != nil {
			t.Error(err)
		}
	}()
	for range 256 {
		f, err := c.Open("/swapped")
		if err == nil {
			buf := make([]byte, 7)
			n, _ := f.Read(buf)
			f.Close()
			if string(buf[:n]) == "secret" {
				t.Fatal("read outside the grant")
			}
		}
		if f, err := c.OpenFile("/swapped", os.O_WRONLY|os.O_TRUNC); err == nil {
			f.Close()
		}
	}
	b, err := os.ReadFile(outside)
	if err != nil || string(b) != "secret" {
		t.Fatalf("outside file changed, %q %v", b, err)
	}
}

func TestCloudNeverSetsPrivilegeBits(t *testing.T) {
	dir := t.TempDir()
	_, c := cloudClient(t, dir, false)
	f, err := c.Create("/program")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	mode := os.FileMode(0755) | os.ModeSetuid | os.ModeSetgid | os.ModeSticky
	check := func() {
		t.Helper()
		info, err := os.Stat(filepath.Join(dir, "program"))
		if err != nil || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || info.Mode().Perm() != 0755 {
			t.Fatalf("unexpected host mode: %v %v", info, err)
		}
	}
	if err := f.Chmod(mode); err != nil {
		t.Fatal(err)
	}
	check()
	if err := c.Chmod("/program", mode); err != nil {
		t.Fatal(err)
	}
	check()
}
