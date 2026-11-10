//go:build windows

package guestos

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// Native ACL fixtures contain only this test's temporary directory. They never
// change a real manager, VM, account, user group or installation directory.
func TestWindowsHostManagerProtectedDirectoryACL(t *testing.T) {
	if os.Getenv("CAGE_TEST_WINDOWS_MANAGER_ACL") != "1" {
		t.Skip("requires explicitly enabled administrator-owned ACL fixtures")
	}
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Fatal("creating administrator-owned ACL fixtures requires an elevated test process")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid := user.User.Sid.String()
	set := func(path, reader, mask string) {
		t.Helper()
		sd, err := windows.SecurityDescriptorFromString("O:BAG:SYD:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;" + mask + ";;;" + reader + ")")
		if err != nil {
			t.Fatal(err)
		}
		owner, _, _ := sd.Owner()
		dacl, _, _ := sd.DACL()
		if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, owner, nil, dacl, nil); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("shared_readers", func(t *testing.T) {
		path := t.TempDir()
		set(path, "S-1-5-32-545", "FRFX")
		if err := managerSharedProtected(path, sid); err != nil {
			t.Fatal("safe shared parent rejected", err)
		}
		if err := managerProtected(path, sid); err == nil {
			t.Fatal("private per-VM authority permits unrelated readers")
		}
	})
	t.Run("delete_child", func(t *testing.T) {
		path := t.TempDir()
		set(path, sid, "0x120040") // READ_CONTROL, SYNCHRONIZE, FILE_DELETE_CHILD.
		if err := managerSharedProtected(path, sid); err == nil {
			t.Fatal("shared parent accepts ordinary-user child replacement")
		}
		if err := managerProtected(path, sid); err == nil {
			t.Fatal("private parent accepts ordinary-user child replacement")
		}
		if err := checkWindowsSystemOwnedObject(path); err == nil {
			t.Fatal("client accepts ordinary-user child replacement")
		}
	})
}
