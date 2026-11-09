package cage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func newStorageFixture(t *testing.T) (string, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(root, "profile")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	return profile, root
}

func writeStorageFixture(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func assertStorageFile(t *testing.T, path string, want []byte) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(b, want) {
		t.Fatalf("storage content mismatch in %s: %v", path, err)
	}
}

func TestStorageMovePreservesLegacyDiskAndColdBackup(t *testing.T) {
	profile, root := newStorageFixture(t)
	file := filepath.Join("lima", "instance", "disk")
	payload := make([]byte, (3<<20)+41)
	copy(payload, []byte("guest header"))
	copy(payload[3<<20:], []byte("guest tail"))
	writeStorageFixture(t, filepath.Join(profile, "state", file), payload)
	writeStorageFixture(t, filepath.Join(profile, "account.json"), []byte("account"))
	target := filepath.Join(root, "中文 folder")
	loc, err := MoveStorage(context.Background(), profile, target, storageExpectation(t, profile), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !loc.Custom || !loc.HasEnvironment || loc.Backup == "" {
		t.Fatal(loc)
	}
	if info, err := os.Stat(filepath.Join(target, "state", file)); err != nil || info.Size() != int64(len(payload)) {
		t.Fatalf("stored disk size: %v %v", info, err)
	}
	assertStorageFile(t, filepath.Join(loc.Backup, file), payload)
	assertStorageFile(t, filepath.Join(profile, "state", file), payload)
	assertStorageFile(t, filepath.Join(profile, "account.json"), []byte("account"))
	old := loc.Backup
	target2 := filepath.Join(root, "second")
	loc, err = MoveStorage(context.Background(), profile, target2, storageExpectation(t, profile), nil)
	if err != nil {
		t.Fatal(err)
	}
	if loc.Backup != target {
		t.Fatal(loc)
	}
	assertStorageFile(t, filepath.Join(target, "state", file), payload)
	assertStorageFile(t, filepath.Join(profile, "state", file), payload)
	paths, err := StoragePrivatePaths(profile)
	if err != nil || len(paths) != 3 || paths[0] != old {
		t.Fatalf("private paths %v, %v", paths, err)
	}
	if err := os.Rename(target2, target2+"-offline"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := AcquireStorageUse(profile); err == nil {
		t.Fatal("started with missing volume")
	}
}

func TestStorageRefusesBusyAndRollsBackCancelledCopy(t *testing.T) {
	profile, root := newStorageFixture(t)
	lease, _, err := AcquireStorageUse(profile)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "new")
	if _, err = MoveStorage(context.Background(), profile, target, storageExpectation(t, profile), nil); err == nil {
		t.Fatal("concurrent move admitted")
	}
	lease.Close()
	instance := filepath.Join(profile, "state", "lima", "vm")
	pid := filepath.Join(instance, "qemu.pid")
	writeStorageFixture(t, pid, []byte(strconv.Itoa(os.Getpid())))
	if _, err = MoveStorage(context.Background(), profile, target, storageExpectation(t, profile), nil); err == nil {
		t.Fatal("running VM admitted")
	}
	if err = os.Remove(pid); err != nil {
		t.Fatal(err)
	}
	writeStorageFixture(t, filepath.Join(instance, "disk"), []byte("original"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err = MoveStorage(ctx, profile, target, storageExpectation(t, profile), func(int64, int64) { cancel() }); err == nil {
		t.Fatal("ignored cancel")
	}
	assertStorageFile(t, filepath.Join(instance, "disk"), []byte("original"))
	if _, err := MoveStorage(context.Background(), profile, target, storageExpectation(t, profile), nil); err != nil {
		t.Fatal("retry after cancellation:", err)
	}
}

func TestStorageFirstUseAndCrashRollback(t *testing.T) {
	profile, root := newStorageFixture(t)
	first := filepath.Join(root, "fresh")
	loc, err := MoveStorage(context.Background(), profile, first, storageExpectation(t, profile), nil)
	if err != nil {
		t.Fatal(err)
	}
	if loc.HasEnvironment || loc.Backup != "" || !loc.Custom {
		t.Fatal(loc)
	}
	payload := []byte("preserved")
	writeStorageFixture(t, filepath.Join(profile, "state", "user-data"), payload)
	target := filepath.Join(root, "interrupted")
	id := "1234567890abcdef1234567890abcdef"
	if err = os.MkdirAll(filepath.Join(target, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = claimStorageTarget(target, id); err != nil {
		t.Fatal(err)
	}
	if err = claimStorageTarget(filepath.Join(target, "state"), id); err != nil {
		t.Fatal(err)
	}
	p := storageMove{Next: storageRecord{Directory: target, ID: id}, PreviousCustom: true}
	if err = writeStorageJSON(filepath.Join(profile, pendingFile), p); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(storageAnchor(profile), filepath.Join(profile, "state-backup-"+id)); err != nil {
		t.Fatal(err)
	}
	if err = linkStorage(storageAnchor(profile), filepath.Join(target, "state")); err != nil {
		t.Fatal(err)
	}
	loc, err = GetStorageLocation(profile)
	if err != nil || loc.Directory != first {
		t.Fatalf("recovery: %+v %v", loc, err)
	}
	assertStorageFile(t, filepath.Join(profile, "state", "user-data"), payload)
	if _, err := MoveStorage(context.Background(), profile, target, storageExpectation(t, profile), nil); err != nil {
		t.Fatal("retry after crash:", err)
	}
}

func TestStorageRollbackLeavesUnownedDestinationUntouched(t *testing.T) {
	profile, root := newStorageFixture(t)
	target := filepath.Join(root, "external")
	id := "abcdef1234567890abcdef1234567890"
	foreign := filepath.Join(target, "state", "user-files")
	writeStorageFixture(t, foreign, []byte("existing"))
	if err := claimStorageTarget(target, id); err != nil {
		t.Fatal(err)
	}
	if err := writeStorageJSON(filepath.Join(profile, pendingFile), storageMove{Next: storageRecord{Directory: target, ID: id}}); err != nil {
		t.Fatal(err)
	}
	if _, err := GetStorageLocation(profile); err != nil {
		t.Fatal(err)
	}
	assertStorageFile(t, foreign, []byte("existing"))
}

func storageExpectation(t *testing.T, profile string) StorageLocation {
	t.Helper()
	loc, err := GetStorageLocation(profile)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// An attached test volume lets native QA exercise a genuine cross-volume
// copy. The parent is never modified beyond the unique temporary folder.
func TestStorageAcrossMountedVolume(t *testing.T) {
	base := os.Getenv("CAGE_TEST_STORAGE_VOLUME")
	if base == "" {
		t.Skip("set CAGE_TEST_STORAGE_VOLUME to a writable second volume")
	}
	profile, _ := newStorageFixture(t)
	testDir, err := os.MkdirTemp(base, "cage-storage-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(testDir) })
	data := make([]byte, 2<<20)
	copy(data[len(data)-32:], []byte("second volume"))
	rel := filepath.Join("lima", "guest", "disk")
	writeStorageFixture(t, filepath.Join(profile, "state", rel), data)
	loc, err := MoveStorage(context.Background(), profile, testDir, storageExpectation(t, profile), nil)
	if err != nil {
		t.Fatal(err)
	}
	assertStorageFile(t, filepath.Join(testDir, "state", rel), data)
	assertStorageFile(t, filepath.Join(loc.Backup, rel), data)
	assertStorageFile(t, filepath.Join(profile, "state", rel), data)
}

func TestStorageRejectsStaleFirstUseDecision(t *testing.T) {
	profile, root := newStorageFixture(t)
	fresh := storageExpectation(t, profile)
	data := []byte("already running user data")
	disk := filepath.Join(profile, "state", "lima", "vm", "disk")
	writeStorageFixture(t, disk, data)
	if _, err := MoveStorage(context.Background(), profile, filepath.Join(root, "target"), fresh, nil); err == nil {
		t.Fatal("silently migrated a disk created after first-use confirmation")
	}
	assertStorageFile(t, disk, data)
}

func TestStorageResolvesAliasesToPrivateDisk(t *testing.T) {
	profile, root := newStorageFixture(t)
	target := filepath.Join(root, "private disk")
	loc, err := MoveStorage(context.Background(), profile, target, storageExpectation(t, profile), nil)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "project-looking-alias")
	if err := linkStorage(alias, loc.Directory); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(alias)
	logical := filepath.Join(alias, "state", "future-file")
	physical, err := StoragePhysicalPath(logical)
	if err != nil || !StorageContains(target, physical) {
		t.Fatalf("private path escaped through alias: %s: %v", physical, err)
	}
}
