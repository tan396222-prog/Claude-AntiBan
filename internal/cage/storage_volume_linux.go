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
	// Only admit filesystems with local disk, private mode and sparse-file
	// semantics. FUSE in particular may represent a remote mount.
	switch uint32(info.Type) {
	case unix.EXT4_SUPER_MAGIC, unix.XFS_SUPER_MAGIC, unix.BTRFS_SUPER_MAGIC, unix.F2FS_SUPER_MAGIC, unix.OVERLAYFS_SUPER_MAGIC, 0x2fc12fc1 /* zfs */, 0xca451a4e /* bcachefs */ :
		return nil
	}
	return errors.New("请选择支持私有权限和稀疏文件的本地磁盘（例如 ext4、XFS 或 Btrfs）")
}
