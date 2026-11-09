package host

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func privateSID() (*windows.SID, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return u.User.Sid, nil
}

func protectPrivate(path string, dir bool) error {
	sid, err := privateSID()
	if err != nil {
		return err
	}
	flags := ""
	if dir {
		flags = "OICI"
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;" + flags + ";FA;;;" + sid.String() + ")(A;" + flags + ";FA;;;SY)")
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	// An owner may replace its DACL even when the parent granted only Modify.
	// Requesting WRITE_OWNER on that path would deny a normal user's install.
	current, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := current.Owner()
	if err != nil {
		return err
	}
	information := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	var newOwner *windows.SID
	if owner == nil || !windows.EqualSid(owner, sid) {
		// An elevated process can create objects owned by Administrators.
		// Transfer them to the user so both normal and elevated launches work.
		information |= windows.OWNER_SECURITY_INFORMATION
		newOwner = sid
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, information, newOwner, nil, acl, nil)
}

func checkPrivate(path string, _ os.FileInfo) error {
	sid, err := privateSID()
	if err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !windows.EqualSid(owner, sid) {
		return errors.New("private record is not owned by the current user")
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount == 0 {
		return errors.New("private record has no usable access restriction")
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			return err
		}
		// Only simple allow ACEs from our protected DACL are accepted. Unknown
		// ACE formats must not accidentally grant access that this check misses.
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return errors.New("private record has an unsupported access rule")
		}
		principal := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !windows.EqualSid(principal, sid) && !principal.IsWellKnown(windows.WinLocalSystemSid) {
			return errors.New("private record is accessible to other users")
		}
	}
	return nil
}
