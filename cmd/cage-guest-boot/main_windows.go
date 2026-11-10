//go:build windows

// cage-guest-boot runs only as a guest-local Windows service. Winlogon owns
// authentication and its LSA secret. This service has no credential, network,
// screen, keyboard or session-control interface.
package main

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"unsafe"

	"github.com/google/uuid"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
)

const winlogon = `SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon`
const bootBase = `SOFTWARE\Cage\WorkbenchBoot\`

var (
	regCreate   = windows.NewLazySystemDLL("advapi32.dll").NewProc("RegCreateKeyExW")
	localGroups = windows.NewLazySystemDLL("netapi32.dll").NewProc("NetUserGetLocalGroups")
	sessionInfo = windows.NewLazySystemDLL("wtsapi32.dll").NewProc("WTSQuerySessionInformationW")
)

type bootService struct {
	vm   uuid.UUID
	user string
}

func read(key registry.Key, name string) string {
	value, _, _ := key.GetStringValue(name)
	return value
}

func (b bootService) boundSID() (string, error) {
	vm, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Virtual Machine\Guest\Parameters`, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer vm.Close()
	id, err := uuid.Parse(read(vm, "VirtualMachineId"))
	if err != nil || id != b.vm {
		return "", errors.New("wrong guest")
	}
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, bootBase+b.vm.String(), registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer key.Close()
	sid := read(key, "SID")
	if read(key, "VM") != b.vm.String() || read(key, "User") != b.user || !strings.HasPrefix(sid, "S-1-5-21-") {
		return "", errors.New("unbound guest user")
	}
	return sid, nil
}

// Require precisely the built-in Users group, including indirect memberships.
// Never arm an account which was renamed, replaced or made an administrator.
func (b bootService) standardUser(expected, host string) bool {
	sid, _, kind, err := windows.LookupSID("", host+`\`+b.user)
	if err != nil || kind != windows.SidTypeUser || sid.String() != expected {
		return false
	}
	user, err := windows.UTF16PtrFromString(b.user)
	if err != nil {
		return false
	}
	var buffer *byte
	var entries, total uint32
	status, _, _ := localGroups.Call(0, uintptr(unsafe.Pointer(user)), 0, 1, uintptr(unsafe.Pointer(&buffer)), uintptr(0xffffffff), uintptr(unsafe.Pointer(&entries)), uintptr(unsafe.Pointer(&total)))
	if buffer != nil {
		defer windows.NetApiBufferFree(buffer)
	}
	if status != 0 || entries != 1 || total != 1 || buffer == nil {
		return false
	}
	groupName := windows.UTF16PtrToString(*(**uint16)(unsafe.Pointer(buffer)))
	groupSID, _, _, err := windows.LookupSID("", groupName)
	return err == nil && groupSID.IsWellKnown(windows.WinBuiltinUsersSid)
}

func (b bootService) configure(enable bool) error {
	expected, err := b.boundSID()
	if err != nil {
		return err
	}
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, winlogon, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	host, err := os.Hostname()
	if err != nil {
		return err
	}
	if read(key, "AutoAdminLogon") == "1" && (!strings.EqualFold(read(key, "DefaultUserName"), b.user) || !strings.EqualFold(read(key, "DefaultDomainName"), host)) {
		return errors.New("another logon policy exists")
	}
	if !enable {
		return key.SetStringValue("AutoAdminLogon", "0")
	}
	if _, _, err := key.GetValue("DefaultPassword", nil); !errors.Is(err, registry.ErrNotExist) {
		return errors.New("existing Winlogon password value")
	}
	if force := read(key, "ForceAutoLogon"); force != "" && force != "0" {
		return errors.New("existing forced logon policy")
	}
	if !b.standardUser(expected, host) {
		if strings.EqualFold(read(key, "DefaultUserName"), b.user) {
			_ = key.SetStringValue("AutoAdminLogon", "0")
		}
		return errors.New("ordinary account required")
	}
	if err = key.SetStringValue("DefaultUserName", b.user); err != nil {
		return err
	}
	if err = key.SetStringValue("DefaultDomainName", host); err != nil {
		return err
	}
	// Arming is the final write; Winlogon reads the protected password itself.
	return key.SetStringValue("AutoAdminLogon", "1")
}

// The volatile key survives a service restart, but not a new boot. A service
// restart after a manual lock or logoff must never rearm the current boot.
func (b bootService) bootMarker() (registry.Key, error) {
	path, _ := windows.UTF16PtrFromString(bootBase + b.vm.String() + `\CurrentBoot`)
	var key windows.Handle
	var disposition uint32
	status, _, _ := regCreate.Call(uintptr(registry.LOCAL_MACHINE), uintptr(unsafe.Pointer(path)), 0, 0, 1, registry.QUERY_VALUE|registry.SET_VALUE, 0, uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&disposition)))
	if status != 0 {
		return 0, syscall.Errno(status)
	}
	return registry.Key(key), nil
}

func signedIn() (bool, error) {
	var sessions *windows.WTS_SESSION_INFO
	var count uint32
	if err := windows.WTSEnumerateSessions(0, 0, 1, &sessions, &count); err != nil {
		return false, err
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(sessions)))
	if count > 4096 {
		return false, errors.New("invalid session count")
	}
	for _, s := range unsafe.Slice(sessions, count) {
		if s.SessionID == 0 || s.State == windows.WTSListen || s.State == windows.WTSDown {
			continue
		}
		var user *uint16
		var size uint32
		ok, _, err := sessionInfo.Call(0, uintptr(s.SessionID), 5, uintptr(unsafe.Pointer(&user)), uintptr(unsafe.Pointer(&size)))
		if ok == 0 {
			return false, err
		}
		present := user != nil && size >= 2 && *user != 0
		windows.WTSFreeMemory(uintptr(unsafe.Pointer(user)))
		if present {
			return true, nil
		}
	}
	return false, nil
}

func (b bootService) Execute(_ []string, requests <-chan svc.ChangeRequest, reports chan<- svc.Status) (bool, uint32) {
	reports <- svc.Status{State: svc.StartPending}
	marker, err := b.bootMarker()
	if err != nil {
		return false, 1
	}
	defer marker.Close()
	status := svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptSessionChange | svc.AcceptPreShutdown}
	reports <- status
	used, _, markerErr := marker.GetIntegerValue("Consumed")
	hasUser, probeError := signedIn()
	if hasUser {
		_ = marker.SetDWordValue("Consumed", 1)
	}
	_ = b.configure(used == 0 && (markerErr == nil || errors.Is(markerErr, registry.ErrNotExist)) && !hasUser && probeError == nil)
	for request := range requests {
		switch request.Cmd {
		case svc.Interrogate:
			reports <- status
		case svc.SessionChange:
			switch request.EventType {
			case windows.WTS_SESSION_LOGON, windows.WTS_SESSION_LOGOFF, windows.WTS_SESSION_LOCK, windows.WTS_SESSION_UNLOCK:
				_ = marker.SetDWordValue("Consumed", 1)
				_ = b.configure(false)
			}
		case svc.PreShutdown:
			// The SCM has committed to a real OS shutdown. Prepare the next
			// boot before the registry hive is unloaded.
			_ = b.configure(true)
			reports <- svc.Status{State: svc.StopPending}
			return false, 0
		case svc.Stop:
			_ = b.configure(false)
			reports <- svc.Status{State: svc.StopPending}
			return false, 0
		}
	}
	return false, 0
}
func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	id, err := uuid.Parse(os.Args[1])
	if err != nil || id == uuid.Nil {
		os.Exit(2)
	}
	b := bootService{vm: id, user: "Cage-" + strings.ReplaceAll(id.String(), "-", "")[:10]}
	if err := svc.Run("Cage-Workbench-Boot-"+id.String(), b); err != nil {
		os.Exit(1)
	}
}
