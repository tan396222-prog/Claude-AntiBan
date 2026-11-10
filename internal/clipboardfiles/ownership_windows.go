package clipboardfiles

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func removeCacheDirectory(root *os.Root, record cacheRecord) error {
	identity, err := root.Stat(".")
	if err != nil {
		return err
	}
	// os.Root denies delete sharing on Windows. Replace it with a handle that
	// can remove this exact directory, and check its identity before deleting.
	if err := root.Close(); err != nil {
		return err
	}
	path, err := windows.UTF16PtrFromString(record.Directory)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(path, windows.DELETE|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(handle), record.Directory)
	defer file.Close()
	current, err := file.Stat()
	if err != nil || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(identity, current) {
		return errors.New("clipboard cache directory identity changed")
	}
	marker := filepath.Join(record.Directory, record.Marker)
	contents, err := os.ReadFile(marker)
	if err != nil || string(contents) != record.ID {
		return errors.New("clipboard cache creation record no longer matches")
	}
	if err := os.Remove(marker); err != nil {
		return err
	}
	discard := byte(1)
	if err := windows.SetFileInformationByHandle(handle, windows.FileDispositionInfo, &discard, 1); err != nil {
		return errors.Join(err, os.WriteFile(marker, []byte(record.ID), 0600))
	}
	return nil
}

func lockCacheStore(file *os.File) (func(), error) {
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped); err != nil {
		return nil, err
	}
	return func() { _ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped) }, nil
}

func cacheOwnerAlive(pid int) bool {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return err == windows.ERROR_ACCESS_DENIED
	}
	defer windows.CloseHandle(handle)
	status, err := windows.WaitForSingleObject(handle, 0)
	return err != nil || status == uint32(windows.WAIT_TIMEOUT)
}
