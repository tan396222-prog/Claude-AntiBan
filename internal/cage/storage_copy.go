package cage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Copy cold VM disks without expanding their zero-filled regions. Hash the
// written copy independently before switching any path used by the runtime.
func copyStorage(ctx context.Context, source, target string, report func(int64, int64)) error {
	var total, copied int64
	var last time.Time
	err := filepath.WalkDir(source, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		return err
	}
	if report != nil {
		report(0, total)
	}
	buf := make([]byte, 1<<20)
	zero := make([]byte, len(buf))
	progress := func(n int) {
		copied += int64(n)
		if report != nil && time.Since(last) > 250*time.Millisecond {
			report(copied, total)
			last = time.Now()
		}
	}
	err = filepath.WalkDir(source, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		out := filepath.Join(target, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.Mkdir(out, info.Mode().Perm()|0700)
		case d.Type().IsRegular():
			return copyStorageFile(ctx, path, out, info, buf, zero, progress)
		case d.Type()&os.ModeSocket != 0:
			// The stopped guest recreates its local IPC sockets on startup.
			return nil
		case d.Type()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if filepath.IsAbs(link) || !filepath.IsLocal(filepath.Clean(filepath.Join(filepath.Dir(rel), link))) {
				return fmt.Errorf("环境包含指向外部的链接，未切换存储：%s", rel)
			}
			return os.Symlink(link, out)
		default:
			return fmt.Errorf("环境含有无法复制的特殊文件，未切换存储：%s", rel)
		}
	})
	if err == nil {
		err = filepath.WalkDir(target, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return syncStorageDir(path)
			}
			return nil
		})
	}
	if err == nil && report != nil {
		report(total, total)
	}
	return err
}

func copyStorageFile(ctx context.Context, source, target string, info fs.FileInfo, buf, zero []byte, progress func(int)) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer out.Close()
	if info.Size() >= 1<<20 {
		if err = sparseStorageFile(out); err != nil {
			return fmt.Errorf("目标磁盘不支持稀疏文件：%w", err)
		}
	}
	hash := sha256.New()
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, e := in.Read(buf)
		if n > 0 {
			data := buf[:n]
			hash.Write(data)
			if bytes.Equal(data, zero[:n]) {
				_, err = out.Seek(int64(n), io.SeekCurrent)
			} else {
				_, err = out.Write(data)
			}
			if err != nil {
				return err
			}
			size += int64(n)
			progress(n)
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
	}
	if size != info.Size() {
		return errors.New("复制期间源文件大小发生变化，已取消迁移")
	}
	if err = out.Truncate(size); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	if _, err = out.Seek(0, io.SeekStart); err != nil {
		return err
	}
	verify := sha256.New()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, e := out.Read(buf)
		if n > 0 {
			verify.Write(buf[:n])
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
	}
	if !bytes.Equal(hash.Sum(nil), verify.Sum(nil)) {
		return fmt.Errorf("文件校验失败：%s", filepath.Base(source))
	}
	if err = os.Chtimes(target, info.ModTime(), info.ModTime()); err != nil {
		return err
	}
	return out.Chmod(info.Mode().Perm())
}
