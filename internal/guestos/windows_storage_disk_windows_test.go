//go:build windows

package guestos

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/sylvinus/agent-vm/internal/cage"
	"golang.org/x/sys/windows"
)

func createWindowsStorageFixtureDisk(t *testing.T, path string) {
	t.Helper()
	createWindowsStorageTestDisk(t, path, "", 0)
}

func createWindowsStorageTestDisk(t *testing.T, path, parent string, flags uint32) {
	t.Helper()
	if unsafe.Sizeof(storageVirtualDiskCreate{}) != 128 {
		t.Fatal("unexpected native VHDX parameter layout")
	}
	parameters := storageVirtualDiskCreate{
		Version: 2, MaximumSize: 8 << 20, BlockSize: 2 << 20,
		SectorSize: 512, PhysicalSector: 4096,
	}
	if parent != "" {
		var err error
		parameters.ParentPath, err = windows.UTF16PtrFromString(parent)
		if err != nil {
			t.Fatal(err)
		}
		parameters.MaximumSize, parameters.ParentType = 0, storageVHDXType
	}
	if err := createWindowsStorageDisk(context.Background(), path, &parameters, flags); err != nil {
		t.Fatal(err)
	}
}

func windowsStorageDiskDigest(t *testing.T, path string) [32]byte {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		t.Fatal(err)
	}
	return [32]byte(hash.Sum(nil))
}

func windowsStorageDiskEntries(t *testing.T, path string) []string {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name()
	}
	return names
}

func TestWindowsStorageDiskIndependentCopiesUntouched(t *testing.T) {
	for _, test := range []struct {
		name  string
		flags uint32
	}{{"dynamic", 0}, {"fixed", 1}} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			source, copied := filepath.Join(root, "source.vhdx"), filepath.Join(root, "copied.vhdx")
			createWindowsStorageTestDisk(t, source, "", test.flags)
			digest := windowsStorageDiskDigest(t, source)
			if err := cage.CopyStorageFile(context.Background(), source, copied); err != nil {
				t.Fatal(err)
			}
			if err := prepareWindowsStorageDisk(context.Background(), source, copied); err != nil {
				t.Fatal(err)
			}
			if windowsStorageDiskDigest(t, source) != digest || windowsStorageDiskDigest(t, copied) != digest {
				t.Fatal("standalone disk bytes changed")
			}
			if _, err := readWindowsStorageDisk(copied); err != nil {
				t.Fatal("copied VHDX could not open with native flags NONE", err)
			}
		})
	}
}

// These fixtures modify only payload blocks and BAT entries of freshly created
// VHDX files. A fully present differencing block does not require a bitmap.
func windowsStorageDiskPayload(t *testing.T, file *os.File, block uint64) (int64, uint64) {
	t.Helper()
	table := make([]byte, 64<<10)
	if _, err := file.ReadAt(table, 192<<10); err != nil {
		t.Fatal(err)
	}
	if string(table[:4]) != "regi" || binary.LittleEndian.Uint32(table[8:]) > 2047 {
		t.Fatal("invalid VHDX region table")
	}
	bat := []byte{0x66, 0x77, 0xc2, 0x2d, 0x23, 0xf6, 0x00, 0x42, 0x9d, 0x64, 0x11, 0x5e, 0x9b, 0xfd, 0x4a, 0x08}
	for index := uint32(0); index < binary.LittleEndian.Uint32(table[8:]); index++ {
		entry := table[16+int(index)*32:]
		if bytes.Equal(entry[:16], bat) {
			position := int64(binary.LittleEndian.Uint64(entry[16:])) + int64(block*8)
			var value [8]byte
			if _, err := file.ReadAt(value[:], position); err != nil {
				t.Fatal(err)
			}
			return position, binary.LittleEndian.Uint64(value[:])
		}
	}
	t.Fatal("VHDX BAT region missing")
	return 0, 0
}

func writeWindowsStorageDiskPayload(t *testing.T, path string, block uint64, data []byte) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	position, value := windowsStorageDiskPayload(t, file, block)
	offset := value & ^uint64((1<<20)-1)
	if value&7 != 6 {
		info, err := file.Stat()
		if err != nil {
			t.Fatal(err)
		}
		offset = uint64(info.Size()+(1<<20)-1) & ^uint64((1<<20)-1)
	}
	if _, err := file.WriteAt(data, int64(offset)); err != nil {
		t.Fatal(err)
	}
	var entry [8]byte
	binary.LittleEndian.PutUint64(entry[:], offset|6)
	if _, err := file.WriteAt(entry[:], position); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
}

func readWindowsStorageDiskPayload(t *testing.T, path string, block uint64) []byte {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	_, value := windowsStorageDiskPayload(t, file, block)
	if value&7 != 6 {
		t.Fatal("materialized VHDX block is not fully present", block, value&7)
	}
	data := make([]byte, 2<<20)
	if _, err := file.ReadAt(data, int64(value & ^uint64((1<<20)-1))); err != nil {
		t.Fatal(err)
	}
	return data
}

func windowsStorageDifferencingFixture(t *testing.T) (root, parent, source, copied string, parentData, childData []byte) {
	t.Helper()
	root = t.TempDir()
	parent, source, copied = filepath.Join(root, "parent.vhdx"), filepath.Join(root, "child.vhdx"), filepath.Join(root, "copied.vhdx")
	createWindowsStorageTestDisk(t, parent, "", 1)
	parentData, childData = make([]byte, 2<<20), make([]byte, 2<<20)
	copy(parentData[4096:], []byte("parent inherited user data"))
	copy(childData[8192:], []byte("child changed user data"))
	writeWindowsStorageDiskPayload(t, parent, 0, parentData)
	createWindowsStorageTestDisk(t, source, parent, 0)
	writeWindowsStorageDiskPayload(t, source, 1, childData)
	if err := cage.CopyStorageFile(context.Background(), source, copied); err != nil {
		t.Fatal(err)
	}
	return
}

func TestWindowsStorageDiskDifferencingCopyIndependent(t *testing.T) {
	root, parent, source, copied, parentData, childData := windowsStorageDifferencingFixture(t)
	beforeEntries := windowsStorageDiskEntries(t, root)
	sourceHash, parentHash := windowsStorageDiskDigest(t, source), windowsStorageDiskDigest(t, parent)
	before, err := readWindowsStorageDisk(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareWindowsStorageDisk(context.Background(), source, copied); err != nil {
		t.Fatal(err)
	}
	after, err := readWindowsStorageDisk(copied)
	if err != nil || before.Subtype != 4 || after.Subtype != 3 || after.Identifier != before.Identifier {
		t.Fatal("differencing disk identity was lost", before, after, err)
	}
	if !bytes.Equal(readWindowsStorageDiskPayload(t, copied, 0), parentData) || !bytes.Equal(readWindowsStorageDiskPayload(t, copied, 1), childData) {
		t.Fatal("materialization changed logical disk data")
	}
	if sourceHash != windowsStorageDiskDigest(t, source) || parentHash != windowsStorageDiskDigest(t, parent) {
		t.Fatal("materialization modified original disk chain")
	}
	if !reflect.DeepEqual(beforeEntries, windowsStorageDiskEntries(t, root)) {
		t.Fatal("materialization left temporary files")
	}
	moved := parent + ".moved"
	if err := os.Rename(parent, moved); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Rename(moved, parent); err != nil {
			t.Error(err)
		}
	}()
	if _, err := readWindowsStorageDisk(source); err == nil {
		t.Fatal("missing-parent fixture still opened")
	}
	if _, err := readWindowsStorageDisk(copied); err != nil {
		t.Fatal("materialized disk retained original parent dependency", err)
	}
}

// The context cancels when the production function registers native-I/O
// cancellation, after CreateVirtualDisk has returned ERROR_IO_PENDING.
type windowsStorageCancelAtWait struct {
	context.Context
	done      chan struct{}
	cancelled atomic.Bool
}

func (ctx *windowsStorageCancelAtWait) Done() <-chan struct{} { return ctx.done }
func (ctx *windowsStorageCancelAtWait) Err() error {
	if ctx.cancelled.Load() {
		return context.Canceled
	}
	return nil
}
func (ctx *windowsStorageCancelAtWait) AfterFunc(callback func()) func() bool {
	ctx.cancelled.Store(true)
	close(ctx.done)
	go callback()
	return func() bool { return false }
}

func TestWindowsStorageDiskCancellationAndReplacementFailure(t *testing.T) {
	for _, mode := range []string{"cancel_before_start", "cancel_native_copy", "replacement_denied"} {
		t.Run(mode, func(t *testing.T) {
			root, parent, source, copied, _, _ := windowsStorageDifferencingFixture(t)
			entries := windowsStorageDiskEntries(t, root)
			sourceHash, copiedHash, parentHash := windowsStorageDiskDigest(t, source), windowsStorageDiskDigest(t, copied), windowsStorageDiskDigest(t, parent)
			ctx := context.Background()
			if mode == "cancel_before_start" {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			if mode == "cancel_native_copy" {
				ctx = &windowsStorageCancelAtWait{Context: ctx, done: make(chan struct{})}
			}
			if mode == "replacement_denied" {
				name, err := windows.UTF16PtrFromString(copied)
				if err != nil {
					t.Fatal(err)
				}
				handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer windows.CloseHandle(handle)
			}
			err := prepareWindowsStorageDisk(ctx, source, copied)
			if mode == "replacement_denied" {
				if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) && !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
					t.Fatal("replacement failure was not reported", err)
				}
			} else if !errors.Is(err, context.Canceled) {
				t.Fatal("native disk cancellation was not reported", err)
			}
			if windowsStorageDiskDigest(t, source) != sourceHash || windowsStorageDiskDigest(t, copied) != copiedHash || windowsStorageDiskDigest(t, parent) != parentHash {
				t.Fatal("failed materialization modified a retained disk")
			}
			if !reflect.DeepEqual(entries, windowsStorageDiskEntries(t, root)) {
				t.Fatal("failed materialization left temporary files")
			}
		})
	}
}
