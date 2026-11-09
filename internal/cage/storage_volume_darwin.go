package cage

import (
	"errors"

	"golang.org/x/sys/unix"
)

func localStorageVolume(path string) error {
	var info unix.Statfs_t
	if err := unix.Statfs(path, &info); err != nil {
		return err
	}
	if info.Flags&unix.MNT_LOCAL == 0 {
		return errors.New("环境存储需要本地磁盘")
	}
	if unix.ByteSliceToString(info.Fstypename[:]) != "apfs" {
		return errors.New("请选择本地 APFS 卷，环境需要私有文件权限和稀疏磁盘支持")
	}
	if info.Flags&unix.MNT_IGNORE_OWNERSHIP != 0 {
		return errors.New("请在 Finder 的磁盘“显示简介”中取消“忽略此宗卷的所有权”，或选择其他 APFS 磁盘")
	}
	return nil
}
