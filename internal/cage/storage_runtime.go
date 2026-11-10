package cage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// StorageTransition identifies one cold move. Runtime state is captured before
// the journal is published and survives process termination with that journal.
type StorageTransition struct {
	ID          string          `json:"id"`
	Source      string          `json:"source"`
	Destination string          `json:"destination"`
	Backup      string          `json:"backup"`
	State       json.RawMessage `json:"state,omitempty"`
}

type StorageRuntimeSnapshot struct {
	State    json.RawMessage
	Backups  []string
	Excluded []string
}

// StorageRuntime coordinates an independently registered VM with the shared
// filesystem transaction. Begin prevents VM starts until Recover completes.
// Switch runs after the verified copy and before the stable directory changes.
// Recovery restores the directory first, then resolves the runtime, and only
// then removes a failed copy. The original data is retained after commit.
type StorageRuntime interface {
	Name() string
	Snapshot(context.Context, string) (StorageRuntimeSnapshot, error)
	Begin(context.Context, StorageTransition) error
	Switch(context.Context, StorageTransition) error
	Recover(context.Context, StorageTransition, bool) error
}

var ErrStorageRuntimeRecovery = errors.New("环境迁移需要客机管理助手完成恢复，请重新打开原工作台")

// RecoverStorage resolves the durable journal using its owning runtime. Plain
// filesystem callers leave a native VM transaction intact until this succeeds.
func RecoverStorage(ctx context.Context, profile string, runtime StorageRuntime) error {
	if _, err := os.Stat(filepath.Join(profile, pendingFile)); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	lock, err := openStorageLockWithRuntime(ctx, profile, true, runtime)
	if err != nil {
		return err
	}
	return lock.Close()
}

// CopyStorage copies an additional runtime-owned directory into a fresh private
// directory. It uses the same sparse-file and digest verification as the move.
func CopyStorage(ctx context.Context, source, target string, excluded []string, report func(int64, int64)) error {
	for _, path := range excluded {
		if !filepath.IsAbs(path) || sameStoragePath(path, source) || !StorageContains(source, path) {
			return errors.New("客机管理文件超出当前存储目录")
		}
	}
	if err := os.Mkdir(target, 0700); err != nil {
		return err
	}
	return copyStorageExcluding(ctx, source, target, excluded, report)
}

func CopyStorageFile(ctx context.Context, source, target string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("环境磁盘需要真实本地文件")
	}
	return copyStorageFile(ctx, source, target, info, make([]byte, 1<<20), make([]byte, 1<<20), func(int) {})
}
