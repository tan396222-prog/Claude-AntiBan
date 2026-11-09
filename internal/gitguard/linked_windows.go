package gitguard

import "golang.org/x/sys/windows"

// linked detects writable aliases of metadata on NTFS as well as Unix.
func linked(p string) bool {
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return false
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	return windows.GetFileInformationByHandle(handle, &info) == nil && info.NumberOfLinks > 1
}
