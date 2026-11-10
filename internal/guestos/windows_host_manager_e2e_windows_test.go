//go:build windows

package guestos

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/google/uuid"
	"golang.org/x/sys/windows"
)

// This test only reads the already registered VM. It must run in the ordinary
// desktop user's token so direct Hyper-V administrator access cannot mask a
// broken manager boundary.
func TestWindowsHostManagerOrdinaryRead(t *testing.T) {
	idText := os.Getenv("CAGE_TEST_WINDOWS_MANAGER_VM")
	if idText == "" {
		t.Skip("requires the installed fixed-VM manager and ordinary host session")
	}
	id, err := uuid.Parse(idText)
	if err != nil {
		t.Fatal("invalid test VM")
	}
	token := windows.GetCurrentProcessToken()
	if token.IsElevated() {
		t.Fatal("this acceptance test must use the ordinary desktop token")
	}
	admins, _ := windows.StringToSid("S-1-5-32-578")
	member, err := token.IsMember(admins)
	if err != nil || member {
		t.Fatal("ordinary host must not receive Hyper-V Administrators membership")
	}
	binding, err := LoadWindowsHostManagerBinding(WindowsHostManagerBindingPath(id))
	if err != nil {
		t.Fatal(err)
	}
	user, err := token.GetTokenUser()
	if err != nil || user.User.Sid.String() != binding.HostSID {
		t.Fatal("acceptance token differs from protected manager host identity")
	}
	for _, path := range []string{WindowsHostManagerBindingPath(id), WindowsHostManagerExecutablePath(id)} {
		name, _ := windows.UTF16PtrFromString(path)
		handle, err := windows.CreateFile(name, windows.FILE_WRITE_DATA|windows.WRITE_DAC, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
		if err == nil {
			windows.CloseHandle(handle)
			t.Fatal("ordinary caller can alter manager code or authority")
		}
		if err != windows.ERROR_ACCESS_DENIED {
			t.Fatal("manager write protection was not proven by access denial", err)
		}
	}
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseServiceHandle(scm)
	serviceName, _ := windows.UTF16PtrFromString(WindowsHostManagerServiceName(id))
	service, err := windows.OpenService(scm, serviceName, windows.SERVICE_QUERY_CONFIG|windows.SERVICE_QUERY_STATUS|windows.SERVICE_START)
	if err != nil {
		t.Fatal("ordinary host cannot query or restore its fixed helper", err)
	}
	windows.CloseServiceHandle(service)
	service, err = windows.OpenService(scm, serviceName, windows.SERVICE_CHANGE_CONFIG|windows.WRITE_DAC)
	if err == nil {
		windows.CloseServiceHandle(service)
		t.Fatal("ordinary host can replace its SYSTEM service configuration")
	}
	if err != windows.ERROR_ACCESS_DENIED {
		t.Fatal("service configuration write denial was not proven", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var inspected HyperVInspection
	if err := windowsHostManagerCall(ctx, id, "inspect", 0, 0, &inspected); err != nil {
		t.Fatal(err)
	}
	if inspected.Lookup != "found" || inspected.VMID != id.String() || inspected.VM == nil || inspected.VM.NetworkAdapterCount != 0 || inspected.VM.Generation != 2 || !inspected.VM.SecureBoot || !inspected.VM.VirtualTPM {
		t.Fatal("manager did not return the registered offline-network VM layout")
	}
	public, err := InspectHyperV(ctx, id)
	if err != nil || public.Lookup != "found" || public.VMID != id.String() || public.VM == nil || public.VM.State != inspected.VM.State {
		t.Fatal("ordinary public inspection did not reach the authenticated fixed-VM manager", err)
	}
	var resources WindowsVMResourceState
	if err := windowsHostManagerCall(ctx, id, "resources", 0, 0, &resources); err != nil {
		t.Fatal(err)
	}
	if resources.State != inspected.VM.State || resources.CPUs <= 0 || resources.MemoryStartup < 4<<30 {
		t.Fatal("manager did not return actual fixed-VM resources")
	}
	t.Logf("ordinary token accepted; VM state=%s, no NIC, CPU=%d, memory=%d GiB", inspected.VM.State, resources.CPUs, resources.MemoryStartup>>30)

	request := func(t *testing.T, body string, expected string) {
		t.Helper()
		conn, err := winio.DialPipeAccessImpLevel(ctx, `\\.\pipe\Cage.HyperV.`+id.String(), 0x120003, winio.PipeImpLevelIdentification)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		if err := verifyWindowsHostManagerPeer(conn, id); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
		line, err := bufio.NewReader(conn).ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		var response windowsHostManagerResponse
		if json.Unmarshal(line, &response) != nil || response.OK || response.Error != expected || response.VMID != id || response.Version != windowsHostManagerVersion || len(response.Result) != 0 {
			t.Fatalf("unsafe or unexpected rejection: %s", line)
		}
	}
	for _, item := range []struct{ name, body, code string }{
		{"wrong_vm", `{"version":1,"vm_id":"` + uuid.NewString() + `","operation":"inspect"}` + "\n", "invalid_request"},
		{"unknown_operation", `{"version":1,"vm_id":"` + id.String() + `","operation":"execute"}` + "\n", "invalid_request"},
		{"unknown_field", `{"version":1,"vm_id":"` + id.String() + `","operation":"inspect","command":"noop"}` + "\n", "invalid_request"},
		{"resource_bounds", `{"version":1,"vm_id":"` + id.String() + `","operation":"resources","memory_gib":1025,"cpus":1}` + "\n", "invalid_request"},
		{"oversize", strings.Repeat("x", windowsHostManagerWireLimit+1) + "\n", "invalid_request"},
		{"extra_json", `{"version":1,"vm_id":"` + id.String() + `","operation":"inspect"} {}` + "\n", "invalid_request"},
	} {
		t.Run(item.name, func(t *testing.T) { request(t, item.body, item.code) })
	}
	// Invalid clients must not poison later reads or stop the existing VM.
	var after HyperVInspection
	if err := windowsHostManagerCall(ctx, id, "inspect", 0, 0, &after); err != nil || after.VM == nil || after.VM.State != inspected.VM.State {
		t.Fatal("rejected requests damaged the manager or VM state", err)
	}
}
