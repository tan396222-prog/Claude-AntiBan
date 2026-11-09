package host

import (
	"testing"

	"golang.org/x/sys/unix"
)

// Block counts are in f_frsize units, which f_bsize (the preferred I/O size)
// may differ from.
func TestBlockSize(t *testing.T) {
	if got := blockSize(&unix.Statfs_t{Bsize: 4096, Frsize: 512}); got != 512 {
		t.Errorf("Frsize set: %d", got)
	}
	if got := blockSize(&unix.Statfs_t{Bsize: 4096}); got != 4096 {
		t.Errorf("Frsize unset: %d", got)
	}
	if _, ok := FreeGiB(t.TempDir()); !ok {
		t.Error("FreeGiB failed")
	}
}
