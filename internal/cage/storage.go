package cage

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/sylvinus/agent-vm/internal/host"
	"github.com/sylvinus/agent-vm/internal/paths"
)

type StorageLocation struct {
	Directory      string `json:"directory"`
	Custom         bool   `json:"custom"`
	HasEnvironment bool   `json:"has_environment"`
	Backup         string `json:"backup,omitempty"`
}

type storageRecord struct {
	Directory string   `json:"directory"`
	ID        string   `json:"id"`
	Backups   []string `json:"backups,omitempty"`
}

const locationFile = "environment-storage.json"
const pendingFile = "environment-storage-pending.json"
const storageMarker = ".cage-storage-id"

type storageMove struct {
	Next storageRecord
	// Previous anchor is retained when the old storage is a directory.
	// If it was a link, only the link is discarded after commit.
	PreviousCustom bool
}

func readStorageRecord(profile string) (storageRecord, error) {
	var r storageRecord
	b, err := os.ReadFile(filepath.Join(profile, locationFile))
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(b, &r); err != nil || !filepath.IsAbs(r.Directory) || !validStorageID(r.ID) {
		return storageRecord{}, errors.New("Cage 环境存储配置损坏，请先恢复配置")
	}
	return r, nil
}

func validStorageID(id string) bool {
	return len(id) == 32 && strings.Trim(id, "0123456789abcdef") == ""
}

func writeStorageJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".storage-config-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	return renameStorage(f.Name(), path)
}

func sameStoragePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// Resolve the existing prefix too: both a new data directory and an offline
// project grant can have a final component which does not exist yet.
func StoragePhysicalPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("请选择绝对路径")
	}
	path = filepath.Clean(path)
	real, err := paths.Resolve(path)
	if err == nil {
		return real, nil
	}
	if !os.IsNotExist(err) || filepath.Dir(path) == path {
		return "", err
	}
	if _, err = os.Lstat(path); err == nil || !os.IsNotExist(err) {
		return "", errors.New("目录包含无法解析的链接")
	}
	parent, err := StoragePhysicalPath(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}

func StorageContains(parent, child string) bool {
	rel, err := filepath.Rel(paths.Fold(filepath.Clean(parent)), paths.Fold(filepath.Clean(child)))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func StoragePathsOverlap(a, b string) bool {
	return StorageContains(a, b) || StorageContains(b, a)
}

// Upgrades replace the entire application folder including its resources.
func runningApplicationDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, err = paths.Resolve(exe)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(exe)
	for parent := dir; parent != filepath.Dir(parent); parent = filepath.Dir(parent) {
		if strings.EqualFold(filepath.Ext(parent), ".app") {
			return parent, nil
		}
	}
	if strings.EqualFold(filepath.Base(dir), "resources") {
		return filepath.Dir(dir), nil
	}
	return dir, nil
}

// StoragePrivatePaths reports every externally located copy, including old
// disks retained for recovery. The main profile itself is already private.
func StoragePrivatePaths(profile string) ([]string, error) {
	r, err := readStorageRecord(profile)
	if err != nil {
		return nil, err
	}
	private := append([]string(nil), r.Backups...)
	if r.Directory != "" {
		private = append(private, r.Directory)
	} else if _, e := os.Readlink(storageAnchor(profile)); e == nil {
		real, e := paths.Resolve(storageAnchor(profile))
		if e != nil {
			return nil, e
		}
		private = append(private, real)
	}
	// A partially copied environment must also be protected from new shares.
	b, err := os.ReadFile(filepath.Join(profile, pendingFile))
	if errors.Is(err, os.ErrNotExist) {
		return private, nil
	}
	if err != nil {
		return nil, err
	}
	var p storageMove
	if err = json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	return append(private, p.Next.Directory), nil
}

func storageAnchor(profile string) string { return filepath.Join(profile, "state") }

func getStorageLocation(profile string) (StorageLocation, error) {
	r, err := readStorageRecord(profile)
	if err != nil {
		return StorageLocation{}, err
	}
	anchor := storageAnchor(profile)
	loc := StorageLocation{Directory: anchor, Custom: r.Directory != ""}
	if loc.Custom {
		loc.Directory = r.Directory
		real, err := paths.Resolve(anchor)
		target, te := paths.Resolve(filepath.Join(r.Directory, "state"))
		marker, me := os.ReadFile(filepath.Join(r.Directory, storageMarker))
		if err != nil || te != nil || me != nil || string(marker) != r.ID || !sameStoragePath(real, target) {
			return loc, fmt.Errorf("环境存储不可用，请重新连接原磁盘：%s", r.Directory)
		}
		if err = localStorageVolume(target); err != nil {
			return loc, err
		}
	} else if _, e := os.Readlink(anchor); e == nil {
		// Older installs may have an operator-managed symlink. Keep their
		// original cache, VM identity and path resolution on ordinary starts.
		real, e := paths.Resolve(anchor)
		if e != nil {
			return loc, e
		}
		fi, e := os.Stat(real)
		if e != nil || !fi.IsDir() {
			return loc, fmt.Errorf("环境目录不可用：%s", anchor)
		}
		if _, e = os.Stat(filepath.Join(filepath.Dir(real), storageMarker)); !errors.Is(e, os.ErrNotExist) {
			return loc, errors.New("环境存储配置缺失，请恢复原有配置")
		}
		loc.Directory = real
	} else if info, err := os.Lstat(anchor); err == nil && !info.IsDir() {
		return loc, fmt.Errorf("环境目录类型异常：%s", anchor)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return loc, err
	}
	if len(r.Backups) > 0 {
		loc.Backup = r.Backups[len(r.Backups)-1]
	}
	entries, err := os.ReadDir(filepath.Join(anchor, "lima"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return loc, err
	}
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), "_") {
			loc.HasEnvironment = true
			break
		}
	}
	return loc, nil
}

func GetStorageLocation(profile string) (StorageLocation, error) {
	lock, err := openStorageLock(profile, false)
	if err != nil {
		return StorageLocation{}, err
	}
	defer lock.Close()
	return getStorageLocation(profile)
}

// AcquireStorageUse holds the same shared lease for the entire Cage command,
// including detached hostagents. A migration cannot run in the launch window.
func AcquireStorageUse(profile string) (*os.File, StorageLocation, error) {
	lock, err := openStorageLock(profile, false)
	if err != nil {
		return nil, StorageLocation{}, err
	}
	loc, err := getStorageLocation(profile)
	if err != nil {
		lock.Close()
		return nil, loc, err
	}
	return lock, loc, nil
}

func openStorageLock(profile string, exclusive bool) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(profile, "environment-storage.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	ok, err := lockStorageFile(f, exclusive)
	if err != nil || !ok {
		f.Close()
		if err == nil {
			err = errors.New("环境正在运行或迁移中，请停止环境并等待当前操作完成")
		}
		return nil, err
	}
	if _, err = os.Stat(filepath.Join(profile, pendingFile)); errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	if !exclusive {
		f.Close()
		// Acquire exclusively before attempting recovery, then reacquire a
		// shared lease. Recheck the journal once more under the new lease.
		w, err := openStorageLock(profile, true)
		if err != nil {
			return nil, err
		}
		w.Close()
		return openStorageLock(profile, false)
	}
	if err = recoverStorage(profile); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func ValidateStorageTarget(profile, directory string) error {
	if _, err := os.Readlink(storageAnchor(profile)); err == nil {
		r, err := readStorageRecord(profile)
		if err != nil {
			return err
		}
		if r.Directory == "" {
			return errors.New("当前环境使用手动配置的目录链接。请先恢复原目录，再使用 Cage 迁移")
		}
	}
	if !filepath.IsAbs(directory) || filepath.Clean(directory) == filepath.VolumeName(directory)+string(os.PathSeparator) {
		return errors.New("请选择本地磁盘上的专用绝对路径，不能使用磁盘根目录")
	}
	target, err := StoragePhysicalPath(directory)
	if err != nil {
		return err
	}
	// Check the actual mount, including junctions and symlinked parents.
	existing := target
	for {
		if _, err = os.Stat(existing); err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) || filepath.Dir(existing) == existing {
			return err
		}
		existing = filepath.Dir(existing)
	}
	if sameStoragePath(existing, target) {
		root, e := storageMountRoot(existing)
		if e != nil {
			return e
		}
		if root {
			return errors.New("请在磁盘内新建专用文件夹，不能使用卷根目录")
		}
	}
	if err = localStorageVolume(existing); err != nil {
		return err
	}
	private, err := StoragePrivatePaths(profile)
	if err != nil {
		return err
	}
	private = append(private, profile, EnvironmentDir(profile))
	app, err := runningApplicationDir()
	if err != nil {
		return err
	}
	private = append(private, app)
	for _, p := range private {
		real, e := StoragePhysicalPath(p)
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if StoragePathsOverlap(directory, p) || (real != "" && StoragePathsOverlap(target, real)) {
			return errors.New("请选择独立空目录，不能与程序、配置、工作区、现有存储或备份重叠")
		}
	}
	if info, e := os.Lstat(directory); e == nil && (!info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0) {
		return errors.New("目标必须是本地真实目录")
	} else if e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return errors.New("环境存储需要专用空目录，请新建文件夹")
	}
	return nil
}

func stoppedStorage(source string) error {
	// Examine every instance, including interrupted base construction.
	instances, err := os.ReadDir(filepath.Join(source, "lima"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, inst := range instances {
		if !inst.IsDir() || strings.HasPrefix(inst.Name(), "_") {
			continue
		}
		dir := filepath.Join(source, "lima", inst.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasSuffix(name, ".pid") && name != "cage-running.json" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				return err
			}
			var pid int
			if name == "cage-running.json" {
				var record runningProfile
				err = json.Unmarshal(data, &record)
				pid = record.PID
			} else {
				pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
			}
			if err != nil || pid < 1 {
				return fmt.Errorf("无法核验虚拟机进程记录 %s", name)
			}
			if profileProcessAlive(pid) {
				return fmt.Errorf("请先停止虚拟机 %s，再迁移环境", inst.Name())
			}
		}
	}
	return nil
}

func MoveStorage(ctx context.Context, profile, directory string, expected StorageLocation, report func(copied, total int64)) (loc StorageLocation, err error) {
	lock, err := openStorageLock(profile, true)
	if err != nil {
		return loc, err
	}
	defer lock.Close()
	loc, err = getStorageLocation(profile)
	if err != nil {
		return loc, err
	}
	if !sameStoragePath(expected.Directory, loc.Directory) || expected.HasEnvironment != loc.HasEnvironment {
		return loc, errors.New("环境或存储位置已改变，请重新选择")
	}
	if err = ValidateStorageTarget(profile, directory); err != nil {
		return loc, err
	}
	directory, err = StoragePhysicalPath(directory)
	if err != nil {
		return loc, err
	}
	anchor := storageAnchor(profile)
	if err = stoppedStorage(anchor); err != nil {
		return loc, err
	}
	previous, err := readStorageRecord(profile)
	if err != nil {
		return loc, err
	}
	var token [16]byte
	if _, err = rand.Read(token[:]); err != nil {
		return loc, err
	}
	id := fmt.Sprintf("%x", token)
	next := storageRecord{Directory: directory, ID: id, Backups: append([]string(nil), previous.Backups...)}
	anchorBackup := filepath.Join(profile, "state-backup-"+id)
	src := anchor
	info, statErr := os.Lstat(anchor)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return loc, statErr
	}
	if statErr == nil {
		backup := anchorBackup
		if loc.Custom {
			backup = loc.Directory
			src = filepath.Join(loc.Directory, "state")
		}
		next.Backups = append(next.Backups, backup)
	}
	move := storageMove{Next: next, PreviousCustom: loc.Custom}
	if err = host.EnsurePrivateDir(directory); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return loc, fmt.Errorf("无法将目标目录设为仅当前用户可访问。请选择自己新建且可以修改权限的空文件夹：%w", err)
		}
		return loc, err
	}
	if err = ValidateStorageTarget(profile, directory); err != nil {
		return loc, err
	}
	if err = writeStorageJSON(filepath.Join(profile, pendingFile), move); err != nil {
		return loc, err
	}
	// Recovery only removes files under the newly claimed target with a
	// matching random identity. It never recursively removes an old copy.
	defer func() {
		if recErr := recoverStorage(profile); recErr != nil {
			err = errors.Join(err, fmt.Errorf("恢复迁移：%w", recErr))
		}
	}()
	if err = claimStorageTarget(directory, id); err != nil {
		return loc, err
	}
	destState := filepath.Join(directory, "state")
	if err = os.Mkdir(destState, 0700); err != nil {
		return loc, err
	}
	// An independently created state directory must never be mistaken for
	// one owned by this transfer when recovering after a failed mkdir.
	if err = claimStorageTarget(destState, id); err != nil {
		_ = os.Remove(destState)
		return loc, err
	}
	if info != nil {
		if err = copyStorage(ctx, src, destState, report); err != nil {
			return loc, err
		}
	}
	staged := filepath.Join(profile, ".storage-link-"+id)
	if err = linkStorage(staged, destState); err != nil {
		return loc, err
	}
	if err = syncStorageDir(directory); err != nil {
		return loc, err
	}
	if err = ctx.Err(); err != nil {
		return loc, err
	}
	if info != nil {
		if err = renameStorage(anchor, anchorBackup); err != nil {
			return loc, err
		}
	}
	if err = renameStorage(staged, anchor); err != nil {
		return loc, err
	}
	if err = writeStorageJSON(filepath.Join(profile, locationFile), next); err != nil {
		return loc, err
	}
	return getStorageLocation(profile)
}

func claimStorageTarget(dir, id string) error {
	f, err := os.OpenFile(filepath.Join(dir, storageMarker), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.WriteString(f, id)
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}

func recoverStorage(profile string) error {
	journal := filepath.Join(profile, pendingFile)
	b, err := os.ReadFile(journal)
	if err != nil {
		return err
	}
	var p storageMove
	if err = json.Unmarshal(b, &p); err != nil {
		return err
	}
	next := p.Next
	if !validStorageID(next.ID) || !filepath.IsAbs(next.Directory) {
		return errors.New("存储迁移记录损坏")
	}
	r, err := readStorageRecord(profile)
	if err != nil {
		return err
	}
	old := filepath.Join(profile, "state-backup-"+next.ID)
	stage := filepath.Join(profile, ".storage-link-"+next.ID)
	anchor := storageAnchor(profile)
	dest := filepath.Join(next.Directory, "state")
	if r.ID == next.ID {
		if token, e := os.ReadFile(filepath.Join(dest, storageMarker)); e == nil && string(token) == next.ID {
			if err = os.Remove(filepath.Join(dest, storageMarker)); err != nil {
				return err
			}
		}
		if p.PreviousCustom {
			if err = os.Remove(old); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	} else {
		if t, e := os.Readlink(anchor); e == nil && sameStoragePath(t, dest) {
			if err = os.Remove(anchor); err != nil {
				return err
			}
		}
		if _, e := os.Lstat(old); e == nil {
			if _, e = os.Lstat(anchor); !errors.Is(e, os.ErrNotExist) {
				return fmt.Errorf("请保留旧环境并检查迁移目录：%s", old)
			}
			if err = renameStorage(old, anchor); err != nil {
				return err
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		// Disconnected destination disks never prevent resuming the old copy.
		marker := filepath.Join(next.Directory, storageMarker)
		if token, e := os.ReadFile(marker); e == nil && string(token) == next.ID {
			if owner, e := os.ReadFile(filepath.Join(dest, storageMarker)); e == nil && string(owner) == next.ID {
				if err = os.RemoveAll(dest); err != nil {
					return err
				}
			}
			if err = os.Remove(marker); err != nil {
				return err
			}
		}
	}
	if err = os.Remove(stage); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Remove(journal)
}
