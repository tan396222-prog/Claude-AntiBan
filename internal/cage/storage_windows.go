package cage

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func lockStorageFile(f *os.File, exclusive bool) (bool, error) {
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, new(windows.Overlapped))
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

func localStorageVolume(path string) error {
	volume := filepath.VolumeName(path)
	if len(volume) != 2 || strings.HasPrefix(path, `\\`) {
		return errors.New("请使用本地磁盘上的文件夹")
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var volumePath [32768]uint16
	if err = windows.GetVolumePathName(name, &volumePath[0], uint32(len(volumePath))); err != nil {
		return err
	}
	root := &volumePath[0]
	switch windows.GetDriveType(root) {
	case windows.DRIVE_FIXED, windows.DRIVE_REMOVABLE:
	default:
		return errors.New("请选择已连接的本地磁盘")
	}
	var flags uint32
	if err = windows.GetVolumeInformation(root, nil, 0, nil, nil, &flags, nil, 0); err != nil {
		return err
	}
	const required = windows.FILE_PERSISTENT_ACLS | windows.FILE_SUPPORTS_SPARSE_FILES
	if flags&required != required {
		return errors.New("请使用支持私有文件权限和稀疏磁盘的本地卷，例如 NTFS")
	}
	return nil
}

// A mount-point reparse point works without Developer Mode or administrator
// privileges. Use the native API so directory names are never shell input.
func linkStorage(link, target string) (err error) {
	sub, err := windows.UTF16FromString(`\??\` + target)
	if err != nil {
		return err
	}
	label, err := windows.UTF16FromString(target)
	if err != nil {
		return err
	}
	data := make([]byte, 16+2*(len(sub)+len(label)))
	if len(data) > 16*1024 {
		return errors.New("存储路径过长")
	}
	binary.LittleEndian.PutUint32(data, windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(data[4:], uint16(len(data)-8))
	binary.LittleEndian.PutUint16(data[10:], uint16(2*(len(sub)-1)))
	binary.LittleEndian.PutUint16(data[12:], uint16(2*len(sub)))
	binary.LittleEndian.PutUint16(data[14:], uint16(2*(len(label)-1)))
	for i, c := range append(sub, label...) {
		binary.LittleEndian.PutUint16(data[16+i*2:], c)
	}
	if err = os.Mkdir(link, 0700); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(link)
		}
	}()
	path, err := windows.UTF16PtrFromString(link)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(path, windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	var n uint32
	return windows.DeviceIoControl(h, windows.FSCTL_SET_REPARSE_POINT, &data[0], uint32(len(data)), nil, 0, &n, nil)
}

func sparseStorageFile(f *os.File) error {
	var n uint32
	return windows.DeviceIoControl(windows.Handle(f.Fd()), windows.FSCTL_SET_SPARSE, nil, 0, nil, 0, &n, nil)
}

func storageMountRoot(path string) (bool, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	var root [32768]uint16
	if err = windows.GetVolumePathName(name, &root[0], uint32(len(root))); err != nil {
		return false, err
	}
	return sameStoragePath(path, windows.UTF16ToString(root[:])), nil
}

// Windows commits the link and record renames with MOVEFILE_WRITE_THROUGH.
// Flushing a directory via an ordinary user handle is unsupported.
func syncStorageDir(string) error { return nil }

func renameStorage(from, to string) error {
	src, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	dst, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(src, dst, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
