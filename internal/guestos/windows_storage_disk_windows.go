//go:build windows

package guestos

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var storageVirtualDiskDLL = windows.NewLazySystemDLL("virtdisk.dll")

type storageVirtualDiskType struct {
	Device uint32
	Vendor windows.GUID
}

var storageVHDXType = storageVirtualDiskType{
	Device: 3,
	Vendor: windows.GUID{Data1: 0xec984aec, Data2: 0xa0f9, Data3: 0x47e9, Data4: [8]byte{0x90, 0x1f, 0x71, 0x41, 0x5a, 0x66, 0x34, 0x5b}},
}

// CREATE_VIRTUAL_DISK_PARAMETERS version 2 has an eight-byte aligned union.
type storageVirtualDiskCreate struct {
	Version        uint32
	_              uint32
	Identifier     windows.GUID
	MaximumSize    uint64
	BlockSize      uint32
	SectorSize     uint32
	PhysicalSector uint32
	ParentPath     *uint16
	SourcePath     *uint16
	OpenFlags      uint32
	ParentType     storageVirtualDiskType
	SourceType     storageVirtualDiskType
	Resiliency     windows.GUID
}

type storageVirtualDiskMetadata struct {
	Subtype        uint32
	Identifier     windows.GUID
	VirtualSize    uint64
	BlockSize      uint32
	SectorSize     uint32
	PhysicalSector uint32
}

func readWindowsStorageDisk(path string) (storageVirtualDiskMetadata, error) {
	var metadata storageVirtualDiskMetadata
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return metadata, err
	}
	parameters := struct {
		Version    uint32
		InfoOnly   int32
		ReadOnly   int32
		Resiliency windows.GUID
	}{Version: 2, InfoOnly: 1, ReadOnly: 1}
	var handle windows.Handle
	status, _, _ := storageVirtualDiskDLL.NewProc("OpenVirtualDisk").Call(
		uintptr(unsafe.Pointer(&storageVHDXType)), uintptr(unsafe.Pointer(name)), 0, 0,
		uintptr(unsafe.Pointer(&parameters)), uintptr(unsafe.Pointer(&handle)))
	runtime.KeepAlive(name)
	if status != 0 {
		return metadata, fmt.Errorf("读取 Windows 客机磁盘失败：%w", windows.Errno(status))
	}
	defer windows.CloseHandle(handle)
	for _, version := range []uint32{1, 2, 7, 10} {
		information := struct {
			Version uint32
			_       uint32
			Data    [4]uint64
		}{Version: version}
		size, used := uint32(unsafe.Sizeof(information)), uint32(0)
		status, _, _ = storageVirtualDiskDLL.NewProc("GetVirtualDiskInformation").Call(
			uintptr(handle), uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&information)), uintptr(unsafe.Pointer(&used)))
		if status != 0 {
			return metadata, fmt.Errorf("读取 Windows 客机磁盘属性失败：%w", windows.Errno(status))
		}
		switch version {
		case 1:
			metadata.VirtualSize = information.Data[0]
			metadata.BlockSize = uint32(information.Data[2])
			metadata.SectorSize = uint32(information.Data[2] >> 32)
		case 2:
			metadata.Identifier = *(*windows.GUID)(unsafe.Pointer(&information.Data[0]))
		case 7:
			metadata.Subtype = uint32(information.Data[0])
		case 10:
			metadata.PhysicalSector = uint32(information.Data[0])
		}
	}
	return metadata, nil
}

// Cancellation must finish native I/O before its pinned OVERLAPPED, file
// handles, or source-disk protection can be released by the migration caller.
func createWindowsStorageDisk(ctx context.Context, path string, parameters *storageVirtualDiskCreate, flags uint32) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(event)
	overlapped := &windows.Overlapped{HEvent: event}
	var pinned runtime.Pinner
	pinned.Pin(overlapped)
	pinned.Pin(parameters)
	pinned.Pin(name)
	if parameters.SourcePath != nil {
		pinned.Pin(parameters.SourcePath)
	}
	if parameters.ParentPath != nil {
		pinned.Pin(parameters.ParentPath)
	}
	defer pinned.Unpin()
	var handle windows.Handle
	status, _, _ := storageVirtualDiskDLL.NewProc("CreateVirtualDisk").Call(
		uintptr(unsafe.Pointer(&storageVHDXType)), uintptr(unsafe.Pointer(name)), 0, 0, uintptr(flags), 0,
		uintptr(unsafe.Pointer(parameters)), uintptr(unsafe.Pointer(overlapped)), uintptr(unsafe.Pointer(&handle)))
	if handle != 0 && handle != windows.InvalidHandle {
		defer func() { err = errors.Join(err, windows.CloseHandle(handle)) }()
	}
	if status != 0 && status != uintptr(windows.ERROR_IO_PENDING) {
		return fmt.Errorf("创建独立 Windows 客机磁盘失败：%w", windows.Errno(status))
	}
	if status == 0 {
		return ctx.Err()
	}
	cancelDone := make(chan struct{})
	var cancelErr error
	stopCancel := context.AfterFunc(ctx, func() {
		cancelErr = windows.CancelIoEx(handle, overlapped)
		close(cancelDone)
	})
	wait, waitErr := windows.WaitForSingleObject(event, windows.INFINITE)
	if !stopCancel() {
		<-cancelDone
	}
	if waitErr != nil || wait != windows.WAIT_OBJECT_0 {
		// A wait failure must still drain the operation before releasing memory.
		_ = windows.CancelIoEx(handle, overlapped)
		var transferred uint32
		drainErr := windows.GetOverlappedResult(handle, overlapped, &transferred, true)
		return errors.Join(fmt.Errorf("等待 Windows 磁盘复制完成失败 (%d)", wait), waitErr, drainErr)
	}
	if errors.Is(cancelErr, windows.ERROR_NOT_FOUND) {
		cancelErr = nil // Completion can win the cancellation race.
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(err, cancelErr)
	}
	progress := struct {
		Status         uint32
		Current, Total uint64
	}{}
	status, _, _ = storageVirtualDiskDLL.NewProc("GetVirtualDiskOperationProgress").Call(
		uintptr(handle), uintptr(unsafe.Pointer(overlapped)), uintptr(unsafe.Pointer(&progress)))
	if status != 0 {
		return fmt.Errorf("查询 Windows 磁盘复制结果失败：%w", windows.Errno(status))
	}
	if progress.Status != 0 {
		return fmt.Errorf("Windows 磁盘复制失败：%w", windows.Errno(progress.Status))
	}
	return nil
}

func prepareWindowsStorageDisk(ctx context.Context, source, copied string) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	before, err := readWindowsStorageDisk(source)
	if err != nil {
		return err
	}
	if before.Subtype == 2 || before.Subtype == 3 {
		return nil
	}
	if before.Subtype != 4 {
		return fmt.Errorf("Windows 客机磁盘类型无效 (%d)", before.Subtype)
	}
	original, err := os.Stat(source)
	if err != nil {
		return err
	}
	destination, err := os.Stat(copied)
	if err != nil {
		return err
	}
	if os.SameFile(original, destination) {
		return errors.New("独立磁盘副本与原磁盘必须使用不同文件")
	}
	folder, err := os.MkdirTemp(filepath.Dir(copied), ".windows-disk-*")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(folder)) }()
	path := filepath.Join(folder, "disk.vhdx")
	name, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	parameters := storageVirtualDiskCreate{
		Version: 2, Identifier: before.Identifier, BlockSize: before.BlockSize,
		SectorSize: before.SectorSize, PhysicalSector: before.PhysicalSector,
		SourcePath: name, SourceType: storageVHDXType,
	}
	if err := createWindowsStorageDisk(ctx, path, &parameters, 0); err != nil {
		return err
	}
	after, err := readWindowsStorageDisk(path)
	if err != nil {
		return err
	}
	expected := before
	expected.Subtype = 3
	if after != expected {
		return errors.New("独立磁盘副本的标识或磁盘参数不一致")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	from, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(copied)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
