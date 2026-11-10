package reversesshfs

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// Open an absolute local drive path from its volume root. DONT_REPARSE applies
// to all remaining components, so junctions cannot redirect the grant.
func openCloudRoot(directory string) (rootedSys, error) {
	dir := filepath.Clean(directory)
	drive := filepath.VolumeName(dir)
	if len(drive) != 2 || drive[1] != ':' {
		return rootedSys{}, errDenied
	}
	base, err := openRootedSys(drive + `\`)
	if err != nil {
		return rootedSys{}, err
	}
	defer base.close()
	h, err := ntCreate(base.rootHandle, strings.TrimPrefix(dir[len(drive):], `\`), fileDirectoryAccess, 0, windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE, windows.OBJ_DONT_REPARSE)
	if err != nil {
		return rootedSys{}, err
	}
	name, err := normalizedName(h)
	if err != nil {
		windows.CloseHandle(h)
		return rootedSys{}, errDenied
	}
	return rootedSys{localPath: dir, rootHandle: h, rootName: name}, nil
}

func (h *rootedHandlers) cloudOpenRead(rel string) (*os.File, error) {
	var fh windows.Handle
	var err error
	if rel == "." {
		self := windows.CurrentProcess()
		err = windows.DuplicateHandle(self, h.rootHandle, self, &fh, 0, false, windows.DUPLICATE_SAME_ACCESS)
	} else {
		fh, err = ntCreate(h.rootHandle, filepath.FromSlash(rel), windows.FILE_GENERIC_READ, 0, windows.FILE_OPEN, 0, windows.OBJ_DONT_REPARSE)
	}
	if err != nil {
		return nil, err
	}
	err = h.checkHandle(fh)
	var fi windows.ByHandleFileInformation
	if err == nil {
		err = windows.GetFileInformationByHandle(fh, &fi)
	}
	if err == nil && (fi.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DEVICE) != 0 || (fi.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 && fi.NumberOfLinks != 1)) {
		err = errDenied
	}
	if err != nil {
		windows.CloseHandle(fh)
		return nil, err
	}
	return os.NewFile(uintptr(fh), rel), nil
}
