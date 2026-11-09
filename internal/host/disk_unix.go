//go:build !windows

package host

import "golang.org/x/sys/unix"

// freeGiB is the space free for an unprivileged user on dir's file system.
func freeGiB(dir string) (int, bool) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir+"/.", &st); err != nil {
		return 0, false
	}
	return int(uint64(st.Bavail) * blockSize(&st) >> 30), true
}
