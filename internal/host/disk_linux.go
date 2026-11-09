package host

import "golang.org/x/sys/unix"

// blockSize is the unit of st's block counts: f_frsize on Linux, where
// f_bsize is only the preferred I/O size.
func blockSize(st *unix.Statfs_t) uint64 {
	if st.Frsize > 0 {
		return uint64(st.Frsize)
	}
	return uint64(st.Bsize)
}
