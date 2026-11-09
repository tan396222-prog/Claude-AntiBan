//go:build !windows && !linux

package host

import "golang.org/x/sys/unix"

// blockSize is the unit of st's block counts: f_bsize on the BSDs and macOS.
func blockSize(st *unix.Statfs_t) uint64 {
	return uint64(st.Bsize)
}
