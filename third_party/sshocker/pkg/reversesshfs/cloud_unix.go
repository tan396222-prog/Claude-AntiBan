//go:build linux || darwin

package reversesshfs

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Start at the volume root, so replacing any component of the picker result
// with a symlink while the grant is being opened cannot redirect it.
func openCloudRoot(directory string) (rootedSys, error) {
	const flags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	fd, err := unix.Open("/", flags, 0)
	if err != nil {
		return rootedSys{}, err
	}
	for _, c := range strings.Split(strings.TrimPrefix(filepath.Clean(directory), "/"), "/") {
		next, err := unix.Openat(fd, c, flags, 0)
		unix.Close(fd)
		if err != nil {
			return rootedSys{}, err
		}
		fd = next
	}
	return rootedSys{rootFD: fd}, nil
}

func (h *rootedHandlers) cloudOpenRead(rel string) (*os.File, error) {
	parent, base, err := h.openParent(rel)
	if err != nil {
		return nil, err
	}
	defer unix.Close(parent)
	fd, err := cloudOpenat(parent, base, unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK|unix.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	err = unix.Fstat(fd, &st)
	if err == nil && st.Mode&unix.S_IFMT != unix.S_IFDIR && (st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1) {
		err = errDenied
	}
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), rel), nil
}
