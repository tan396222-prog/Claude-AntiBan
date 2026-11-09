package host

import "golang.org/x/sys/windows"

// freeGiB is the space free for this user on dir's volume.
func freeGiB(dir string) (int, bool) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, false
	}
	var free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, nil, nil); err != nil {
		return 0, false
	}
	return int(free >> 30), true
}
