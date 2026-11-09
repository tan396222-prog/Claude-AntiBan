//go:build !windows

package gitguard

import (
	"os"
	"syscall"
)

// linked reports whether the regular file p has other hard links.
func linked(p string) bool {
	fi, err := os.Lstat(p)
	if err != nil {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && st.Nlink > 1
}
