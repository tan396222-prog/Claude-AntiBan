//go:build windows

package guestos

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"github.com/google/uuid"
	"golang.org/x/sys/windows"

	"github.com/sylvinus/agent-vm/internal/paths"
)

// WindowsHostManagerBinding is installed by an administrator. The service never
// takes its authority from a user's workbench profile or a pipe request.
type WindowsHostManagerBinding struct {
	Version    int       `json:"version"`
	VMID       uuid.UUID `json:"vm_id"`
	HostSID    string    `json:"host_sid"`
	DiskPath   string    `json:"disk_path"`
	DiskVolume uint32    `json:"disk_volume"`
	DiskFileID uint64    `json:"disk_file_id"`
}

func WindowsHostManagerServiceName(id uuid.UUID) string { return "CageHV-" + id.String() }

func managerKnownFolder(id *windows.KNOWNFOLDERID) string {
	p, err := windows.KnownFolderPath(id, 0)
	if err != nil {
		return ""
	}
	return p
}

func WindowsHostManagerBindingPath(id uuid.UUID) string {
	return filepath.Join(managerKnownFolder(windows.FOLDERID_ProgramData), "Cage", "HyperVManager", id.String(), "binding.json")
}

func WindowsHostManagerExecutablePath(id uuid.UUID) string {
	return filepath.Join(managerKnownFolder(windows.FOLDERID_ProgramFiles), "Cage Host Manager", id.String(), "Cage.HyperVManager.exe")
}

func managerProtected(path, hostSID string) error {
	return managerProtectedAccess(path, hostSID, false)
}

func managerSharedProtected(path, hostSID string) error {
	return managerProtectedAccess(path, hostSID, true)
}

func managerProtectedAccess(path, hostSID string, shared bool) error {
	for p := path; p != filepath.Dir(p); p = filepath.Dir(p) {
		name, err := windows.UTF16PtrFromString(p)
		if err != nil {
			return errors.New("binding_invalid")
		}
		attributes, err := windows.GetFileAttributes(name)
		if err != nil || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return errors.New("binding_invalid")
		}
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return errors.New("binding_invalid")
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !(owner.IsWellKnown(windows.WinLocalSystemSid) || owner.IsWellKnown(windows.WinBuiltinAdministratorsSid)) {
		return errors.New("binding_invalid")
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return errors.New("binding_invalid")
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount == 0 {
		return errors.New("binding_invalid")
	}
	for index := uint32(0); index < uint32(acl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, index, &ace) != nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return errors.New("binding_invalid")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
			continue
		}
		const deleteChild = 0x40 // FILE_DELETE_CHILD is absent from x/sys/windows.
		const modify = windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_EA | windows.FILE_WRITE_ATTRIBUTES | deleteChild | windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER | windows.GENERIC_WRITE | windows.GENERIC_ALL
		if (!shared && sid.String() != hostSID) || ace.Mask&modify != 0 {
			return errors.New("binding_invalid")
		}
	}
	return nil
}

func LoadWindowsHostManagerBinding(path string) (WindowsHostManagerBinding, error) {
	var binding WindowsHostManagerBinding
	file, err := os.Open(path)
	if err != nil {
		return binding, errors.New("binding_invalid")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > windowsHostManagerWireLimit {
		return binding, errors.New("binding_invalid")
	}
	decoder := json.NewDecoder(io.LimitReader(file, windowsHostManagerWireLimit+1))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&binding) != nil || decoder.Decode(new(any)) != io.EOF || binding.Version != windowsHostManagerVersion || binding.VMID == uuid.Nil || binding.DiskFileID == 0 || binding.DiskVolume == 0 || !filepath.IsAbs(binding.DiskPath) || strings.HasPrefix(binding.DiskPath, `\\`) || !strings.EqualFold(filepath.Ext(binding.DiskPath), ".vhdx") {
		return binding, errors.New("binding_invalid")
	}
	sid, err := windows.StringToSid(binding.HostSID)
	if err != nil || !(strings.HasPrefix(sid.String(), "S-1-5-21-") || strings.HasPrefix(sid.String(), "S-1-12-1-")) || !strings.EqualFold(filepath.Clean(path), WindowsHostManagerBindingPath(binding.VMID)) {
		return binding, errors.New("binding_invalid")
	}
	if err = managerProtected(path, binding.HostSID); err != nil {
		return binding, err
	}
	if err = managerProtected(filepath.Dir(path), binding.HostSID); err != nil {
		return binding, err
	}
	if err = managerSharedProtected(filepath.Dir(filepath.Dir(path)), binding.HostSID); err != nil {
		return binding, err
	}
	if err = managerSharedProtected(filepath.Dir(filepath.Dir(filepath.Dir(path))), binding.HostSID); err != nil {
		return binding, err
	}
	return binding, nil
}

func managerDiskIdentity(path string) (uint32, uint64, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, errors.New("vm_changed")
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return 0, 0, errors.New("vm_changed")
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(handle, &info) != nil || info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
		return 0, 0, errors.New("vm_changed")
	}
	return info.VolumeSerialNumber, uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow), nil
}

func managerVMDisk(ctx context.Context, id uuid.UUID) (string, error) {
	script := fmt.Sprintf("$ErrorActionPreference='Stop';$v=Get-VM -Id ([guid]'%s');$d=@(Get-VMHardDiskDrive -VM $v);if($d.Count -ne 1){throw 'bound_disk_invalid'};[pscustomobject]@{path=$d[0].Path}|ConvertTo-Json -Compress", id.String())
	cmd, err := systemPowerShellQuery(ctx, script)
	if err != nil {
		return "", errors.New("vm_changed")
	}
	data, err := cmd.Output()
	if err != nil || len(data) > 16384 {
		return "", errors.New("vm_changed")
	}
	var disk struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(data, &disk) != nil {
		return "", errors.New("vm_changed")
	}
	return disk.Path, nil
}

func managerValidateVM(ctx context.Context, binding WindowsHostManagerBinding) (HyperVInspection, error) {
	state, err := inspectHyperVNative(ctx, binding.VMID)
	if err != nil || state.Lookup != "found" || state.VM == nil || state.VM.Generation != 2 || !state.VM.SecureBoot || !state.VM.VirtualTPM || state.VM.NetworkAdapterCount != 0 || state.VM.DiskCount != 1 || state.VM.SingleLocalVHDX == nil || !*state.VM.SingleLocalVHDX {
		return state, errors.New("vm_changed")
	}
	disk, err := managerVMDisk(ctx, binding.VMID)
	if err != nil {
		return state, err
	}
	real, err := paths.Resolve(disk)
	if err != nil || !strings.EqualFold(real, binding.DiskPath) {
		return state, errors.New("vm_changed")
	}
	volume, id, err := managerDiskIdentity(real)
	if err != nil || volume != binding.DiskVolume || id != binding.DiskFileID {
		return state, errors.New("vm_changed")
	}
	return state, nil
}

func PrepareWindowsHostManagerBinding(ctx context.Context, id uuid.UUID, hostSID string) (WindowsHostManagerBinding, error) {
	var binding WindowsHostManagerBinding
	token := windows.GetCurrentProcessToken()
	host, err := windows.StringToSid(hostSID)
	if err != nil || !token.IsElevated() || !(strings.HasPrefix(host.String(), "S-1-5-21-") || strings.HasPrefix(host.String(), "S-1-12-1-")) {
		return binding, errors.New("installation_requires_administrator")
	}
	state, err := inspectHyperVNative(ctx, id)
	if err != nil || state.Lookup != "found" || state.VM == nil || state.VM.Generation != 2 || !state.VM.SecureBoot || !state.VM.VirtualTPM || state.VM.NetworkAdapterCount != 0 || state.VM.DiskCount != 1 {
		return binding, errors.New("vm_changed")
	}
	disk, err := managerVMDisk(ctx, id)
	if err != nil {
		return binding, err
	}
	real, err := paths.Resolve(disk)
	if err != nil || strings.HasPrefix(real, `\\`) || !strings.EqualFold(filepath.Ext(real), ".vhdx") {
		return binding, errors.New("vm_changed")
	}
	volume, fileID, err := managerDiskIdentity(real)
	if err != nil {
		return binding, err
	}
	return WindowsHostManagerBinding{Version: windowsHostManagerVersion, VMID: id, HostSID: hostSID, DiskPath: real, DiskVolume: volume, DiskFileID: fileID}, nil
}

func managerClientSID(conn net.Conn) (string, error) {
	file, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return "", errors.New("unauthorized")
	}
	var pid uint32
	if windows.GetNamedPipeClientProcessId(windows.Handle(file.Fd()), &pid) != nil || pid == 0 {
		return "", errors.New("unauthorized")
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", errors.New("unauthorized")
	}
	defer windows.CloseHandle(process)
	var token windows.Token
	if windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token) != nil {
		return "", errors.New("unauthorized")
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", errors.New("unauthorized")
	}
	return user.User.Sid.String(), nil
}

type windowsHostManagerServer struct {
	binding WindowsHostManagerBinding
	mutate  sync.RWMutex
}

func managerHostMemoryGiB() int {
	var memory struct {
		Length, Load                                                                                                  uint32
		TotalPhysical, AvailablePhysical, TotalPage, AvailablePage, TotalVirtual, AvailableVirtual, AvailableExtended uint64
	}
	memory.Length = uint32(unsafe.Sizeof(memory))
	result, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx").Call(uintptr(unsafe.Pointer(&memory)))
	if result == 0 {
		return 0
	}
	return int(memory.TotalPhysical >> 30)
}

func (server *windowsHostManagerServer) request(ctx context.Context, request windowsHostManagerRequest, conn net.Conn) (any, error) {
	if request.validate() != nil || request.VMID != server.binding.VMID {
		return nil, errors.New("invalid_request")
	}
	if request.Operation == "storage" || request.Operation == "start" || request.Operation == "shutdown" || request.Operation == "resources" && request.CPUs != 0 {
		server.mutate.Lock()
		defer server.mutate.Unlock()
	} else {
		server.mutate.RLock()
		defer server.mutate.RUnlock()
	}
	journal, err := server.loadStorageJournal()
	if err != nil {
		return nil, err
	}
	if request.Storage != nil {
		return server.storageRequest(ctx, *request.Storage, journal, conn)
	}
	if journal != nil && journal.Phase == "pending" {
		return nil, errors.New("storage_pending")
	}
	state, err := managerValidateVM(ctx, server.binding)
	if err != nil {
		return nil, err
	}
	name, err := windows.UTF16PtrFromString(server.binding.DiskPath)
	if err != nil {
		return nil, errors.New("vm_changed")
	}
	// Hold the verified inode across the operation. A same-user caller cannot
	// replace the VHDX path after validation while the SYSTEM operation runs.
	lockedDisk, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, errors.New("vm_changed")
	}
	defer windows.CloseHandle(lockedDisk)
	var pinned windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(lockedDisk, &pinned) != nil || pinned.VolumeSerialNumber != server.binding.DiskVolume || uint64(pinned.FileIndexHigh)<<32|uint64(pinned.FileIndexLow) != server.binding.DiskFileID {
		return nil, errors.New("vm_changed")
	}
	switch request.Operation {
	case "inspect":
		return state, nil
	case "resources":
		if request.CPUs != 0 {
			maxMemory, maxCPUs := WindowsVMResourceCeiling(managerHostMemoryGiB(), runtime.NumCPU())
			if request.CPUs > maxCPUs || request.MemoryGiB > maxMemory {
				return nil, errors.New("resources_out_of_bounds")
			}
		}
		return windowsVMResourcesNative(ctx, server.binding.VMID, request.MemoryGiB, request.CPUs)
	case "start", "shutdown":
		action := "start"
		if request.Operation == "shutdown" {
			action = "stop"
		}
		if err := windowsVMActionNative(ctx, server.binding.VMID, action); err != nil {
			return nil, errors.New("operation_failed")
		}
		if _, err = managerValidateVM(ctx, server.binding); err != nil {
			return nil, err
		}
		return windowsVMResourcesNative(ctx, server.binding.VMID, 0, 0)
	}
	return nil, errors.New("invalid_operation")
}

func (server *windowsHostManagerServer) connection(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	response := windowsHostManagerResponse{Version: windowsHostManagerVersion, VMID: server.binding.VMID}
	write := func() {
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		_ = json.NewEncoder(conn).Encode(response)
	}
	sid, err := managerClientSID(conn)
	if err != nil || sid != server.binding.HostSID {
		response.Error = "unauthorized"
		write()
		return
	}
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	reader := bufio.NewReaderSize(conn, windowsHostManagerWireLimit+1)
	line, err := reader.ReadSlice('\n')
	if err != nil || len(line) > windowsHostManagerWireLimit || reader.Buffered() != 0 {
		response.Error = "invalid_request"
		write()
		return
	}
	var request windowsHostManagerRequest
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF {
		response.Error = "invalid_request"
		write()
		return
	}
	conn.SetReadDeadline(time.Time{})
	deadline := time.Minute
	if request.Storage != nil {
		deadline = 3 * time.Minute
	}
	operation, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	// One request per pipe. A client disconnect or extra command cancels work.
	disconnected := make(chan struct{})
	go func() { defer close(disconnected); var extra [1]byte; _, _ = conn.Read(extra[:]); cancel() }()
	result, err := server.request(operation, request, conn)
	if err != nil {
		response.Error = "operation_failed"
		for _, code := range []string{"invalid_request", "invalid_operation", "vm_changed", "resources_out_of_bounds", "storage_pending", "vm_running"} {
			if err.Error() == code {
				response.Error = code
			}
		}
		if operation.Err() != nil {
			response.Error = "cancelled"
		}
	} else {
		response.Result, err = json.Marshal(result)
		response.OK = err == nil
		if err != nil {
			response.Error = "operation_failed"
		}
	}
	write()
	conn.Close()
	<-disconnected
}

func ServeWindowsHostManager(parent context.Context, path string) error {
	return serveWindowsHostManager(parent, path, nil)
}

func serveWindowsHostManager(parent context.Context, path string, ready chan<- struct{}) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || !user.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		return errors.New("service_requires_system")
	}
	binding, err := LoadWindowsHostManagerBinding(path)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil || !strings.EqualFold(exe, WindowsHostManagerExecutablePath(binding.VMID)) {
		return errors.New("binding_invalid")
	}
	if err = managerProtected(exe, binding.HostSID); err != nil {
		return err
	}
	if err = managerProtected(filepath.Dir(exe), binding.HostSID); err != nil {
		return err
	}
	if err = managerSharedProtected(filepath.Dir(filepath.Dir(exe)), binding.HostSID); err != nil {
		return err
	}
	server := windowsHostManagerServer{binding: binding}
	listener, err := winio.ListenPipe(`\\.\pipe\Cage.HyperV.`+binding.VMID.String(), &winio.PipeConfig{SecurityDescriptor: "O:SYG:SYD:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;0x12019b;;;" + binding.HostSID + ")", InputBufferSize: windowsHostManagerWireLimit, OutputBufferSize: windowsHostManagerWireLimit})
	if err != nil {
		return errors.New("service_pipe_unavailable")
	}
	defer listener.Close()
	if ready != nil {
		close(ready)
	}
	var active sync.WaitGroup
	var connections sync.Map
	go func() {
		<-ctx.Done()
		listener.Close()
		connections.Range(func(key, _ any) bool { key.(net.Conn).Close(); return true })
	}()
	slots := make(chan struct{}, 16)
	defer func() {
		cancel()
		listener.Close()
		connections.Range(func(key, _ any) bool { key.(net.Conn).Close(); return true })
		active.Wait()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return errors.New("service_pipe_unavailable")
		}
		select {
		case slots <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		connections.Store(conn, struct{}{})
		active.Add(1)
		go func() {
			defer active.Done()
			defer func() { <-slots; connections.Delete(conn) }()
			server.connection(ctx, conn)
		}()
	}
}
