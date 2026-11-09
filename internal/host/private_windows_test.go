package host

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPrivateFileRejectsEveryoneReadACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(path, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	sid, err := privateSID()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + sid.String() + ")(A;;FR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivateFile(path); err == nil {
		t.Fatal("publicly readable credential accepted")
	}
	if err := ProtectFile(path); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivateFile(path); err != nil {
		t.Fatalf("restricted DACL rejected: %v", err)
	}
}

// Ownership grants WRITE_DAC but does not automatically grant WRITE_OWNER.
// Explicitly withholding WRITE_OWNER makes this regression deterministic even
// when the test runner's temporary directory normally grants full control.
func TestProtectPrivateDoesNotRequireChangingAnExistingOwner(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "selected")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	sid, err := privateSID()
	if err != nil {
		t.Fatal(err)
	}
	current, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := current.Owner()
	if err != nil {
		t.Fatal(err)
	}
	if !windows.EqualSid(owner, sid) {
		if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, sid, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(D;;WO;;;" + sid.String() + ")(A;;FA;;;" + sid.String() + ")(A;;FA;;;SY)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatalf("owner cannot secure directory: %v", err)
	}
	if err := checkPrivate(dir, nil); err != nil {
		t.Fatal(err)
	}
}

func TestProtectPrivateAdoptsAdministratorOwnedFile(t *testing.T) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("requires an elevated Windows token")
	}
	path := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	admin, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, admin, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivateFile(path); err == nil {
		t.Fatal("group-owned file treated as private before repair")
	}
	if err := ProtectFile(path); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivateFile(path); err != nil {
		t.Fatal(err)
	}
}
