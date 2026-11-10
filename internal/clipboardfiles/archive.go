// Package clipboardfiles streams selected ordinary files between guest systems.
// It has no host/guest process or authentication policy; callers own the selection.
package clipboardfiles

import (
	"archive/tar"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const FileLimit int64 = 2 << 30
const SelectionLimit = 100

// LinkCachedFiles groups already frozen guest-cache files for one native file
// chooser without copying every attachment again. GUI callers validate ownership.
func LinkCachedFiles(paths []string) (result []string, err error) {
	return linkCachedFiles(paths, temporaryDirectory)
}

func temporaryDirectory() (string, error) { return os.MkdirTemp("", "cage-clipboard-") }

func linkCachedFiles(paths []string, directory func() (string, error)) (result []string, err error) {
	if len(paths) == 0 || len(paths) > SelectionLimit {
		return nil, errors.New("请选择 1 至 100 个文件")
	}
	root, err := directory()
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(root)
		}
	}()
	for _, source := range paths {
		name := filepath.Base(source)
		info, err := os.Lstat(source)
		if err != nil || !filepath.IsAbs(source) || !strings.HasPrefix(filepath.Base(filepath.Dir(source)), "cage-clipboard-") || !validName(name) || !info.Mode().IsRegular() || info.Size() > FileLimit {
			return nil, errors.New("附件缓存文件无效")
		}
		target := filepath.Join(root, name)
		if err := os.Link(source, target); err != nil {
			return nil, errors.New("无法准备附件选择，请核对文件名是否重复")
		}
		linked, err := os.Lstat(target)
		if err != nil || !linked.Mode().IsRegular() || !os.SameFile(info, linked) {
			return nil, errors.New("附件缓存已变化，请重新选择文件")
		}
		result = append(result, target)
	}
	return result, nil
}

// Save receives one file into a newly owned cache directory. The caller can
// publish the returned path only after the complete bounded write succeeds.
func Save(input io.Reader, name string) (path string, err error) {
	return save(input, name, temporaryDirectory)
}

func save(input io.Reader, name string, directory func() (string, error)) (path string, err error) {
	if !validName(name) || strings.Contains(name, ":") {
		return "", errors.New("剪贴板文件名称无效")
	}
	root, err := directory()
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(root)
		}
	}()
	path = filepath.Join(root, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	count, writeErr := io.Copy(file, io.LimitReader(input, FileLimit+1))
	if count > FileLimit {
		writeErr = errors.New("每个文件不能超过 2 GB")
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if err = errors.Join(writeErr, file.Close()); err != nil {
		return "", err
	}
	return path, nil
}

func validName(name string) bool {
	return name != "" && len(name) <= 255 && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\x00") &&
		!strings.ContainsFunc(name, func(r rune) bool { return r < 32 || r == 127 })
}

func Write(output io.Writer, paths []string) error {
	if len(paths) == 0 || len(paths) > SelectionLimit {
		return errors.New("请选择 1 至 100 个文件或文件夹")
	}
	writer := tar.NewWriter(output)
	names := map[string]bool{}
	for _, selected := range paths {
		name := filepath.Base(selected)
		if !filepath.IsAbs(selected) || !validName(name) || names[name] {
			return errors.New("剪贴板文件名称无效或重复")
		}
		names[name] = true
		err := filepath.Walk(selected, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return errors.New("无法读取所选剪贴板文件")
			}
			if !(info.Mode().IsRegular() || info.IsDir()) || info.Size() > FileLimit {
				return errors.New("剪贴板支持普通文件和文件夹，每个文件最多 2 GB")
			}
			relative, err := filepath.Rel(filepath.Dir(selected), path)
			if err != nil {
				return err
			}
			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			header.Name = filepath.ToSlash(relative)
			if err := writer.WriteHeader(header); err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			file, err := os.Open(path)
			if err != nil {
				return errors.New("无法读取所选剪贴板文件")
			}
			defer file.Close()
			after, err := file.Stat()
			if err != nil || !os.SameFile(info, after) || after.Size() != info.Size() {
				return errors.New("文件已变化，请重新复制")
			}
			_, err = io.CopyN(writer, file, info.Size())
			return err
		})
		if err != nil {
			return err
		}
	}
	return writer.Close()
}

func Read(input io.Reader) (paths []string, err error) {
	return read(input, temporaryDirectory)
}

func read(input io.Reader, directory func() (string, error)) (paths []string, err error) {
	root, err := directory()
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(root)
		}
	}()
	reader := tar.NewReader(input)
	names := map[string]bool{}
	for {
		header, readErr := reader.Next()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
		name := strings.TrimSuffix(header.Name, "/")
		parts := strings.Split(name, "/")
		if !filepath.IsLocal(filepath.FromSlash(name)) || header.Size < 0 || header.Size > FileLimit {
			return nil, errors.New("剪贴板文件位置或大小无效")
		}
		for _, part := range parts {
			if !validName(part) || strings.Contains(part, ":") {
				return nil, errors.New("剪贴板文件名称无效")
			}
		}
		if !names[parts[0]] {
			names[parts[0]] = true
			paths = append(paths, filepath.Join(root, parts[0]))
			if len(paths) > SelectionLimit {
				return nil, errors.New("剪贴板文件数量超过 100 个")
			}
		}
		destination := filepath.Join(root, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return nil, err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err = os.MkdirAll(destination, 0700); err != nil {
				return nil, err
			}
		case tar.TypeReg, tar.TypeRegA:
			file, createErr := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(header.Mode)&0700|0600)
			if createErr != nil {
				return nil, createErr
			}
			_, copyErr := io.CopyN(file, reader, header.Size)
			closeErr := file.Close()
			if copyErr != nil || closeErr != nil {
				return nil, errors.Join(copyErr, closeErr)
			}
		default:
			return nil, errors.New("剪贴板不支持链接或特殊文件")
		}
	}
	if len(paths) == 0 {
		return nil, errors.New("剪贴板文件为空")
	}
	return paths, nil
}

func Copy(paths []string) ([]string, error) {
	return copyFiles(paths, temporaryDirectory)
}

func copyFiles(paths []string, directory func() (string, error)) ([]string, error) {
	reader, writer := io.Pipe()
	defer reader.Close()
	done := make(chan error, 1)
	go func() { err := Write(writer, paths); writer.CloseWithError(err); done <- err }()
	copied, err := read(reader, directory)
	reader.Close()
	if writeErr := <-done; writeErr != nil {
		if len(copied) > 0 {
			_ = os.RemoveAll(filepath.Dir(copied[0]))
		}
		return nil, writeErr
	}
	return copied, err
}
