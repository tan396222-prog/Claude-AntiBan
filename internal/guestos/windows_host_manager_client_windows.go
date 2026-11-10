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
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"github.com/google/uuid"
	"golang.org/x/sys/windows"
)

func windowsHostManagerCall(ctx context.Context, id uuid.UUID, operation string, memory, cpus int, result any) error {
	request := windowsHostManagerRequest{Version: windowsHostManagerVersion, VMID: id, Operation: operation, MemoryGiB: memory, CPUs: cpus}
	return windowsHostManagerRequestCall(ctx, request, result)
}

func windowsHostManagerRequestCall(ctx context.Context, request windowsHostManagerRequest, result any) error {
	id := request.VMID
	if err := request.validate(); err != nil {
		return errors.New("Windows 客机管理参数无效")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	connect, finish := context.WithTimeout(ctx, 10*time.Second)
	level := winio.PipeImpLevelIdentification
	if request.Storage != nil && request.Storage.Action == "attach" {
		// The verified service opens the destination using this caller's rights.
		level = winio.PipeImpLevelImpersonation
	}
	connection, err := winio.DialPipeAccessImpLevel(connect, `\\.\pipe\Cage.HyperV.`+id.String(), 0x120003, level)
	finish()
	if err != nil && ctx.Err() == nil {
		if startErr := startRegisteredWindowsHostManager(id); startErr == nil {
			retry, finish := context.WithTimeout(ctx, 10*time.Second)
			connection, err = winio.DialPipeAccessImpLevel(retry, `\\.\pipe\Cage.HyperV.`+id.String(), 0x120003, level)
			finish()
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("Windows 系统管理助手尚未连接，请在 Cage 安装流程中完成系统授权或恢复助手；原虚拟机和数据已保留")
	}
	defer connection.Close()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	if err := verifyWindowsHostManagerPeer(connection, id); err != nil {
		return err
	}
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return errors.New("Windows 系统管理请求没有完整送达")
	}
	line, err := bufio.NewReaderSize(connection, windowsHostManagerWireLimit).ReadSlice('\n')
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("Windows 系统管理结果没有完整返回，现有环境已保留")
	}
	var response windowsHostManagerResponse
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&response) != nil || decoder.Decode(new(any)) != io.EOF || response.Version != windowsHostManagerVersion || response.VMID != id {
		return errors.New("Windows 系统管理响应身份或格式不匹配")
	}
	if err := verifyWindowsHostManagerPeer(connection, id); err != nil {
		return err
	}
	if !response.OK || response.Error != "" {
		if response.Error == "storage_pending" {
			return errors.New("Windows 客机有待恢复的存储迁移，请重新打开原工作台完成恢复")
		}
		if response.Error == "vm_running" {
			return errors.New("请等待 Windows 客机完整关机，再迁移环境存储")
		}
		if response.Error == "unauthorized" || response.Error == "permission_denied" {
			return errors.New("当前系统用户没有这台 Cage 客机的管理授权")
		}
		if response.Error == "binding_mismatch" || response.Error == "layout_mismatch" || response.Error == "binding_invalid" {
			return errors.New("Windows 客机的登记配置已变化，请核对原虚拟机；现有数据已保留")
		}
		return errors.New("Windows 系统管理操作未完成，请重试或在设置中导出诊断包；现有环境已保留")
	}
	if len(response.Result) == 0 || bytes.Equal(bytes.TrimSpace(response.Result), []byte("null")) || result == nil || json.Unmarshal(response.Result, result) != nil {
		return errors.New("Windows 系统管理响应缺少实际读回结果")
	}
	return nil
}

func verifyWindowsHostManagerPeer(connection net.Conn, id uuid.UUID) error {
	f, ok := connection.(interface{ Fd() uintptr })
	if !ok {
		return errors.New("Windows 系统管理通道无法核验")
	}
	var pid uint32
	if windows.GetNamedPipeServerProcessId(windows.Handle(f.Fd()), &pid) != nil || pid == 0 {
		return errors.New("Windows 系统管理进程无法核验")
	}
	// Query the SCM with read-only rights. A normal user cannot open the
	// LocalSystem process token; the protected service account and live PID
	// provide the corresponding identity without requesting that privilege.
	manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return errors.New("Windows 系统管理服务身份无法读取")
	}
	defer windows.CloseServiceHandle(manager)
	name, _ := windows.UTF16PtrFromString("CageHV-" + id.String())
	service, err := windows.OpenService(manager, name, windows.SERVICE_QUERY_CONFIG|windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return errors.New("Windows 系统管理服务未登记或当前用户不可读取")
	}
	defer windows.CloseServiceHandle(service)
	var status windows.SERVICE_STATUS_PROCESS
	var needed uint32
	if windows.QueryServiceStatusEx(service, windows.SC_STATUS_PROCESS_INFO, (*byte)(unsafe.Pointer(&status)), uint32(unsafe.Sizeof(status)), &needed) != nil || status.ProcessId != pid || status.CurrentState != windows.SERVICE_RUNNING {
		return errors.New("Windows 系统管理通道不属于当前已登记服务")
	}
	return verifyWindowsHostManagerService(service, id)
}

func verifyWindowsHostManagerService(service windows.Handle, id uuid.UUID) error {
	binaryPath, account, err := windowsHostServiceConfig(service)
	if err != nil {
		return errors.New("Windows 系统管理服务配置无法核验")
	}
	if !strings.EqualFold(account, "LocalSystem") && !strings.EqualFold(account, `NT AUTHORITY\SYSTEM`) {
		return errors.New("Windows 系统管理服务未使用受限系统入口")
	}
	program, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if err != nil {
		return err
	}
	expected := filepath.Join(program, "Cage Host Manager", id.String(), "Cage.HyperVManager.exe")
	data, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil {
		return err
	}
	binding := filepath.Join(data, "Cage", "HyperVManager", id.String(), "binding.json")
	arguments, err := windows.DecomposeCommandLine(binaryPath)
	if err != nil || len(arguments) != 4 || !strings.EqualFold(filepath.Clean(arguments[0]), filepath.Clean(expected)) || arguments[1] != "service" || arguments[2] != "--binding" || !strings.EqualFold(filepath.Clean(arguments[3]), filepath.Clean(binding)) {
		return errors.New("Windows 系统管理程序不属于已登记的安装位置")
	}
	if err := checkWindowsSystemOwnedObject(binding); err != nil {
		return err
	}
	for current := expected; !strings.EqualFold(current, program); current = filepath.Dir(current) {
		if current == filepath.Dir(current) {
			return fmt.Errorf("Windows 系统管理程序路径无效")
		}
		if err := checkWindowsSystemOwnedObject(current); err != nil {
			return err
		}
	}
	return nil
}

// Installation already grants the bound user SERVICE_START. Restoring that
// fixed service after a reboot does not grant VM discovery or service editing.
func startRegisteredWindowsHostManager(id uuid.UUID) error {
	binding, err := LoadWindowsHostManagerBinding(WindowsHostManagerBindingPath(id))
	if err != nil {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user.User.Sid.String() != binding.HostSID {
		return errors.New("unauthorized")
	}
	manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return err
	}
	defer windows.CloseServiceHandle(manager)
	name, _ := windows.UTF16PtrFromString(WindowsHostManagerServiceName(id))
	service, err := windows.OpenService(manager, name, windows.SERVICE_QUERY_CONFIG|windows.SERVICE_START)
	if err != nil {
		return err
	}
	defer windows.CloseServiceHandle(service)
	if err = verifyWindowsHostManagerService(service, id); err != nil {
		return err
	}
	err = windows.StartService(service, 0, nil)
	if errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return nil
	}
	return err
}

func windowsHostServiceConfig(service windows.Handle) (string, string, error) {
	// QUERY_SERVICE_CONFIG contains pointers into this native output buffer.
	// A variable-sized allocation stays on the heap when later Go calls grow
	// the stack; copy its strings before releasing the retained buffer.
	size := uint32(1024)
	for size <= windowsHostManagerWireLimit {
		buffer := make([]byte, size)
		config := (*windows.QUERY_SERVICE_CONFIG)(unsafe.Pointer(&buffer[0]))
		var needed uint32
		err := windows.QueryServiceConfig(service, config, size, &needed)
		if err == nil {
			binaryPath, account := windows.UTF16PtrToString(config.BinaryPathName), windows.UTF16PtrToString(config.ServiceStartName)
			runtime.KeepAlive(buffer)
			return binaryPath, account, nil
		}
		runtime.KeepAlive(buffer)
		if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) || needed <= size {
			return "", "", err
		}
		size = needed
	}
	return "", "", errors.New("Windows service configuration exceeds limit")
}

func checkWindowsSystemOwnedObject(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return errors.New("Windows 系统管理程序或目录不可读")
	}
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes, err := windows.GetFileAttributes(ptr)
	if err != nil || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || !info.Mode().IsRegular() && !info.IsDir() {
		return errors.New("Windows 系统管理程序不能经过目录链接")
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return errors.New("Windows 系统管理程序权限不可读")
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.IsWellKnown(windows.WinLocalSystemSid) && !owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
		return errors.New("Windows 系统管理程序未受系统权限保护")
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount == 0 {
		return errors.New("Windows 系统管理程序没有访问限制")
	}
	const fileDeleteChild = 0x40 // FILE_DELETE_CHILD is absent from x/sys/windows.
	const writes = windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_EA | windows.FILE_WRITE_ATTRIBUTES | fileDeleteChild | windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER | windows.GENERIC_WRITE | windows.GENERIC_ALL
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, i, &ace) != nil {
			return errors.New("Windows 系统管理程序权限无法核验")
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return errors.New("Windows 系统管理程序含未支持的权限规则")
		}
		principal := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if ace.Mask&writes != 0 && !principal.IsWellKnown(windows.WinLocalSystemSid) && !principal.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
			return errors.New("Windows 系统管理程序可被普通用户修改")
		}
	}
	return nil
}
