package host

import (
	"errors"
	"os"
)

// EnsurePrivateDir creates host-only storage and removes inherited access.
// Windows uses an explicit DACL; POSIX mode bits do not protect Windows files.
func EnsurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return errors.New("private storage must be a real directory")
	}
	return protectPrivate(path, true)
}

// ProtectFile restricts an existing file or local socket to the current user.
func ProtectFile(path string) error { return protectPrivate(path, false) }

// CheckPrivateFile rejects non-regular files and access by other local users.
func CheckPrivateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("private record must be a regular file")
	}
	return checkPrivate(path, info)
}
