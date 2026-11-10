//go:build windows

package guestos

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/sys/windows"

	"github.com/sylvinus/agent-vm/internal/cage"
)

//go:embed windows_storage.ps1
var windowsStorageScript string

type windowsHostStorageJournal struct {
	ID                  string                     `json:"id"`
	Phase               string                     `json:"phase"`
	Before              WindowsHostManagerBinding  `json:"before"`
	OldDisk             string                     `json:"old_disk"`
	OldLayout           windowsVMStorageLayout     `json:"old_layout"`
	After               *WindowsHostManagerBinding `json:"after,omitempty"`
	NewLayout           *windowsVMStorageLayout    `json:"new_layout,omitempty"`
	Backup              string                     `json:"backup"`
	RegistrationStarted bool                       `json:"registration_started,omitempty"`
}

func (server *windowsHostManagerServer) storageJournalPath() string {
	return filepath.Join(filepath.Dir(WindowsHostManagerBindingPath(server.binding.VMID)), "storage.json")
}

func (server *windowsHostManagerServer) storageRegistrationLayout() windowsVMStorageLayout {
	root := filepath.Join(filepath.Dir(server.storageJournalPath()), "machine")
	return windowsVMStorageLayout{Configuration: filepath.Join(root, "configuration"), Snapshots: filepath.Join(root, "snapshots"), Paging: filepath.Join(root, "paging")}
}

func (server *windowsHostManagerServer) loadStorageJournal() (*windowsHostStorageJournal, error) {
	path := server.storageJournalPath()
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("binding_invalid")
	}
	defer file.Close()
	if err = managerProtected(path, server.binding.HostSID); err != nil {
		return nil, err
	}
	var journal windowsHostStorageJournal
	decoder := json.NewDecoder(io.LimitReader(file, windowsHostManagerWireLimit+1))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&journal) != nil || decoder.Decode(new(any)) != io.EOF || !validWindowsStorageID(journal.ID) ||
		journal.Before.VMID != server.binding.VMID || journal.Before.HostSID != server.binding.HostSID ||
		journal.Phase != "pending" && journal.Phase != "commit" && journal.Phase != "rollback" ||
		journal.NewLayout == nil || *journal.NewLayout != server.storageRegistrationLayout() || journal.Backup != filepath.Join(filepath.Dir(path), "storage-backup-"+journal.ID) ||
		journal.Before.DiskVolume == 0 || journal.Before.DiskFileID == 0 {
		return nil, errors.New("binding_invalid")
	}
	if journal.After != nil && (journal.After.VMID != server.binding.VMID || journal.After.HostSID != server.binding.HostSID ||
		journal.After.DiskVolume == 0 || journal.After.DiskFileID == 0) {
		return nil, errors.New("binding_invalid")
	}
	return &journal, nil
}

func managerProtectStoragePath(path, sid string) error {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("binding_invalid")
	}
	inherit := ""
	if info.IsDir() {
		inherit = "OICI"
	}
	sd, err := windows.SecurityDescriptorFromString("O:SYG:SYD:P(A;" + inherit + ";FA;;;SY)(A;" + inherit + ";FA;;;BA)(A;" + inherit + ";FRFX;;;" + sid + ")")
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, owner, nil, acl, nil)
}

func managerWriteStorageJSON(path, sid string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".storage-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err = managerProtectStoragePath(file.Name(), sid); err != nil {
		file.Close()
		return err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	if err = errors.Join(err, file.Close()); err != nil {
		return err
	}
	from, _ := windows.UTF16PtrFromString(file.Name())
	to, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func managerStorageCommand(ctx context.Context, id uuid.UUID, action string, input map[string]any, result any) error {
	if input == nil {
		input = make(map[string]any)
	}
	input["vm_id"], input["action"] = id.String(), action
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	script := "$inputData = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('" + base64.StdEncoding.EncodeToString(data) + "')) | ConvertFrom-Json\n" + windowsStorageScript
	command, err := systemPowerShellQuery(ctx, script)
	if err != nil {
		return err
	}
	output, err := command.Output()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || len(output) > windowsHostManagerWireLimit || json.Unmarshal(output, result) != nil {
		return errors.New("storage_operation_failed")
	}
	return nil
}

func (server *windowsHostManagerServer) storageState(journal *windowsHostStorageJournal, outcome string) windowsHostStorageState {
	state := windowsHostStorageState{Disk: windowsStorageDisk{Path: server.binding.DiskPath, Volume: server.binding.DiskVolume, FileID: server.binding.DiskFileID}, Outcome: outcome}
	if journal != nil && journal.Phase == "pending" {
		state.Pending = journal.ID
	}
	return state
}

func managerRequireStopped(ctx context.Context, binding WindowsHostManagerBinding) error {
	state, err := managerValidateVM(ctx, binding)
	if err != nil {
		return err
	}
	if state.VM.State != "Off" {
		return errors.New("vm_running")
	}
	return nil
}

func (server *windowsHostManagerServer) storageRequest(ctx context.Context, input windowsHostStorageRequest, journal *windowsHostStorageJournal, conn net.Conn) (any, error) {
	if input.Action == "status" {
		state := server.storageState(journal, "")
		if journal == nil || journal.Phase != "pending" {
			if err := managerRequireStopped(ctx, server.binding); err != nil {
				return nil, err
			}
			var layout windowsVMStorageLayout
			if err := managerStorageCommand(ctx, server.binding.VMID, "inspect", nil, &layout); err != nil {
				return nil, err
			}
			state.Layout = &layout
		}
		return state, nil
	}
	if journal != nil && journal.Phase == "pending" && journal.ID != input.ID {
		return nil, errors.New("storage_pending")
	}
	if journal == nil || journal.Phase != "pending" {
		if journal != nil && journal.ID == input.ID && journal.Phase == input.Action {
			if _, err := managerValidateVM(ctx, server.binding); err != nil {
				return nil, err
			}
			return server.storageState(journal, input.Action), nil
		}
		if server.binding.DiskVolume != input.SourceVolume || server.binding.DiskFileID != input.SourceFileID {
			return nil, errors.New("vm_changed")
		}
		if input.Action == "rollback" {
			if _, err := managerValidateVM(ctx, server.binding); err != nil {
				return nil, err
			}
			return server.storageState(nil, "rollback"), nil
		}
		if input.Action != "begin" {
			return nil, errors.New("invalid_request")
		}
		if err := managerRequireStopped(ctx, server.binding); err != nil {
			return nil, err
		}
		oldDisk, err := managerVMDisk(ctx, server.binding.VMID)
		if err != nil {
			return nil, err
		}
		var layout windowsVMStorageLayout
		if err = managerStorageCommand(ctx, server.binding.VMID, "inspect", nil, &layout); err != nil {
			return nil, err
		}
		backup := filepath.Join(filepath.Dir(server.storageJournalPath()), "storage-backup-"+input.ID)
		registration := server.storageRegistrationLayout()
		journal = &windowsHostStorageJournal{ID: input.ID, Phase: "pending", Before: server.binding, OldDisk: oldDisk, OldLayout: layout, NewLayout: &registration, Backup: backup}
		if err = managerWriteStorageJSON(server.storageJournalPath(), server.binding.HostSID, journal); err != nil {
			return nil, err
		}
	}
	if journal.Before.DiskVolume != input.SourceVolume || journal.Before.DiskFileID != input.SourceFileID {
		return nil, errors.New("vm_changed")
	}
	switch input.Action {
	case "begin":
		if err := server.ensureStorageBackup(ctx, journal); err != nil {
			return nil, err
		}
		if err := server.prepareStorageRegistration(ctx, journal); err != nil {
			return nil, err
		}
	case "attach":
		if err := server.attachStorage(ctx, journal, input, conn); err != nil {
			return nil, err
		}
	case "commit", "rollback":
		if err := server.finishStorage(ctx, journal, input.Action); err != nil {
			return nil, err
		}
	}
	return server.storageState(journal, input.Action), nil
}

// Registration belongs to the fixed system helper. Keeping it on the system
// volume lets rollback reattach the original VHDX even when the target volume
// disappears. VM identity and the exported recovery metadata remain available.
func (server *windowsHostManagerServer) prepareStorageRegistration(ctx context.Context, journal *windowsHostStorageJournal) error {
	root := filepath.Dir(journal.NewLayout.Configuration)
	if err := os.Mkdir(root, 0700); err == nil {
		if err := managerProtectStoragePath(root, server.binding.HostSID); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrExist) {
		return err
	}
	if err := managerProtected(root, server.binding.HostSID); err != nil {
		return err
	}
	if !journal.RegistrationStarted {
		journal.RegistrationStarted = true
		if err := managerWriteStorageJSON(server.storageJournalPath(), server.binding.HostSID, journal); err != nil {
			return err
		}
	}
	return server.applyStorage(ctx, journal, journal.Before, *journal.NewLayout)
}

func (server *windowsHostManagerServer) ensureStorageBackup(ctx context.Context, journal *windowsHostStorageJournal) error {
	complete := filepath.Join(journal.Backup, "cage-export-complete.json")
	if data, err := os.ReadFile(complete); err == nil && bytes.Equal(data, []byte(`{"id":"`+journal.ID+`"}`)) {
		return managerProtected(complete, server.binding.HostSID)
	}
	if journal.After != nil {
		return errors.New("binding_invalid")
	}
	if err := managerRequireStopped(ctx, journal.Before); err != nil {
		return err
	}
	// This directory is created only below the protected fixed-VM registration.
	// Retry a partial metadata export without touching the original VHDX.
	if err := os.RemoveAll(journal.Backup); err != nil {
		return err
	}
	if err := os.Mkdir(journal.Backup, 0700); err != nil {
		return err
	}
	if err := managerProtectStoragePath(journal.Backup, server.binding.HostSID); err != nil {
		return err
	}
	var result struct {
		Exported bool `json:"exported"`
	}
	if err := managerStorageCommand(ctx, server.binding.VMID, "export", map[string]any{"backup": journal.Backup}, &result); err != nil || !result.Exported {
		return errors.Join(err, errors.New("storage_export_failed"))
	}
	return managerWriteStorageJSON(complete, server.binding.HostSID, map[string]string{"id": journal.ID})
}

// Open the requested object while impersonating the authenticated pipe caller.
// The resulting handle proves the caller can read, write and manage its ACL.
// No privileged open is used as a fallback for a denied destination.
func managerOpenStorageDestination(conn net.Conn, path, hostSID string, directory bool) (windows.Handle, windowsStorageDisk, error) {
	var value windowsStorageDisk
	if !filepath.IsAbs(path) || strings.HasPrefix(path, `\\`) || (!directory && !strings.EqualFold(filepath.Ext(path), ".vhdx")) {
		return 0, value, errors.New("invalid_request")
	}
	file, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return 0, value, errors.New("unauthorized")
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, value, err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	impersonate := windows.NewLazySystemDLL("advapi32.dll").NewProc("ImpersonateNamedPipeClient")
	okValue, _, callErr := impersonate.Call(file.Fd())
	if okValue == 0 {
		return 0, value, callErr
	}
	flags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if directory {
		flags |= windows.FILE_FLAG_BACKUP_SEMANTICS
	}
	var token windows.Token
	identityErr := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY, true, &token)
	if identityErr == nil {
		user, err := token.GetTokenUser()
		token.Close()
		if err != nil || user.User.Sid.String() != hostSID {
			identityErr = errors.New("unauthorized")
		}
	}
	var handle windows.Handle
	openErr := identityErr
	if identityErr == nil {
		handle, openErr = windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.WRITE_DAC|windows.WRITE_OWNER, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, flags, 0)
	}
	if revertErr := windows.RevertToSelf(); revertErr != nil {
		if openErr == nil {
			windows.CloseHandle(handle)
		}
		// Continuing this OS thread with a client's token is unsafe.
		panic("Windows storage impersonation could not be reverted")
	}
	if openErr != nil {
		return 0, value, errors.New("destination_access_denied")
	}
	value, err = managerStorageObject(handle, directory)
	if err != nil {
		windows.CloseHandle(handle)
		return 0, value, err
	}
	return handle, value, nil
}

// Pin each canonical directory until the privileged operation finishes. A
// caller may rename a parent between requests, but cannot retarget this request
// through a replaced directory or an NTFS junction after its access check.
func managerPinStorageParents(path string) (func(), error) {
	var handles []windows.Handle
	closeAll := func() {
		for _, handle := range handles {
			windows.CloseHandle(handle)
		}
		handles = nil
	}
	var parents []string
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		parents = append(parents, parent)
		if parent == filepath.Dir(parent) {
			break
		}
	}
	for index := len(parents) - 1; index >= 0; index-- {
		parent := parents[index]
		name, err := windows.UTF16PtrFromString(parent)
		if err != nil {
			closeAll()
			return nil, err
		}
		handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
		if err != nil {
			closeAll()
			return nil, err
		}
		handles = append(handles, handle)
		actual, err := managerStorageObject(handle, true)
		if err != nil || !strings.EqualFold(filepath.Clean(actual.Path), filepath.Clean(parent)) {
			closeAll()
			return nil, errors.New("vm_changed")
		}
	}
	return closeAll, nil
}

func managerStorageObject(handle windows.Handle, directory bool) (windowsStorageDisk, error) {
	var value windowsStorageDisk
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(handle, &info) != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != directory {
		return value, errors.New("vm_changed")
	}
	name := make([]uint16, 32768)
	size, err := windows.GetFinalPathNameByHandle(handle, &name[0], uint32(len(name)), 0)
	if err != nil || size == 0 || size >= uint32(len(name)) {
		return value, errors.New("vm_changed")
	}
	path := strings.TrimPrefix(windows.UTF16ToString(name[:size]), `\\?\`)
	if len(filepath.VolumeName(path)) != 2 || strings.HasPrefix(path, `\\`) {
		return value, errors.New("vm_changed")
	}
	return windowsStorageDisk{Path: path, Volume: info.VolumeSerialNumber, FileID: uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow)}, nil
}

func managerGrantVMDisk(handle windows.Handle, id uuid.UUID) error {
	sid, _, _, err := windows.LookupSID("", `NT VIRTUAL MACHINE\`+id.String())
	if err != nil {
		return err
	}
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	var pin runtime.Pinner
	pin.Pin(sid)
	defer pin.Unpin()
	// FILE_ALL_ACCESS from the Windows SDK; x/sys/windows does not export it.
	const fileAllAccess = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff
	merged, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{AccessPermissions: fileAllAccess, AccessMode: windows.GRANT_ACCESS, Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_USER, TrusteeValue: windows.TrusteeValueFromSID(sid)}}}, acl)
	if err != nil {
		return err
	}
	return windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, merged, nil)
}

func (server *windowsHostManagerServer) attachStorage(ctx context.Context, journal *windowsHostStorageJournal, input windowsHostStorageRequest, conn net.Conn) error {
	if !journal.RegistrationStarted {
		return errors.New("invalid_request")
	}
	if err := server.ensureStorageBackup(ctx, journal); err != nil {
		return err
	}
	diskHandle, disk, err := managerOpenStorageDestination(conn, input.Disk, server.binding.HostSID, false)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(diskHandle)
	diskParents, err := managerPinStorageParents(disk.Path)
	if err != nil {
		return err
	}
	defer diskParents()
	if disk.Volume == journal.Before.DiskVolume && disk.FileID == journal.Before.DiskFileID {
		return errors.New("invalid_request")
	}
	if journal.After == nil {
		after := journal.Before
		after.DiskPath, after.DiskVolume, after.DiskFileID = disk.Path, disk.Volume, disk.FileID
		journal.After = &after
		if err = managerWriteStorageJSON(server.storageJournalPath(), server.binding.HostSID, journal); err != nil {
			return err
		}
	} else if journal.After.DiskPath != disk.Path || journal.After.DiskVolume != disk.Volume || journal.After.DiskFileID != disk.FileID {
		return errors.New("vm_changed")
	}
	if err = managerGrantVMDisk(diskHandle, server.binding.VMID); err != nil {
		return err
	}
	if err = server.applyStorage(ctx, journal, *journal.After, *journal.NewLayout); err != nil {
		return err
	}
	return server.saveStorageBinding(*journal.After)
}

func (server *windowsHostManagerServer) applyStorage(ctx context.Context, journal *windowsHostStorageJournal, binding WindowsHostManagerBinding, layout windowsVMStorageLayout) error {
	name, err := windows.UTF16PtrFromString(binding.DiskPath)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	disk, err := managerStorageObject(handle, false)
	if err != nil || !strings.EqualFold(disk.Path, binding.DiskPath) || disk.Volume != binding.DiskVolume || disk.FileID != binding.DiskFileID {
		return errors.New("vm_changed")
	}
	parents, err := managerPinStorageParents(binding.DiskPath)
	if err != nil {
		return err
	}
	defer parents()
	for _, path := range []string{layout.Configuration, layout.Snapshots, layout.Paging} {
		real, err := cage.StoragePhysicalPath(path)
		if err != nil || !filepath.IsAbs(path) || !strings.EqualFold(real, filepath.Clean(path)) {
			return errors.New("vm_changed")
		}
	}
	var result struct {
		Applied bool `json:"applied"`
	}
	newDisk := journal.Before.DiskPath
	if journal.After != nil {
		newDisk = journal.After.DiskPath
	}
	if err = managerStorageCommand(ctx, server.binding.VMID, "apply", map[string]any{"old": journal.OldLayout, "next": journal.NewLayout, "target": layout, "old_disk": journal.OldDisk, "source_disk": journal.Before.DiskPath, "new_disk": newDisk, "disk": binding.DiskPath}, &result); err != nil || !result.Applied {
		return errors.Join(err, errors.New("storage_operation_failed"))
	}
	return managerRequireStopped(ctx, binding)
}

func (server *windowsHostManagerServer) saveStorageBinding(binding WindowsHostManagerBinding) error {
	if err := managerWriteStorageJSON(WindowsHostManagerBindingPath(binding.VMID), binding.HostSID, binding); err != nil {
		return err
	}
	server.binding.DiskPath, server.binding.DiskVolume, server.binding.DiskFileID = binding.DiskPath, binding.DiskVolume, binding.DiskFileID
	return nil
}

func (server *windowsHostManagerServer) finishStorage(ctx context.Context, journal *windowsHostStorageJournal, action string) error {
	if action == "commit" {
		if journal.After == nil {
			return errors.New("invalid_request")
		}
		if err := managerRequireStopped(ctx, *journal.After); err != nil {
			return err
		}
		var actual windowsVMStorageLayout
		if err := managerStorageCommand(ctx, server.binding.VMID, "inspect", nil, &actual); err != nil || actual != *journal.NewLayout {
			return errors.Join(err, errors.New("vm_changed"))
		}
		if err := server.saveStorageBinding(*journal.After); err != nil {
			return err
		}
	} else {
		if journal.RegistrationStarted {
			if err := server.applyStorage(ctx, journal, journal.Before, *journal.NewLayout); err != nil {
				return err
			}
		} else if err := managerRequireStopped(ctx, journal.Before); err != nil {
			return err
		}
		if err := server.saveStorageBinding(journal.Before); err != nil {
			return err
		}
	}
	journal.Phase = action
	return managerWriteStorageJSON(server.storageJournalPath(), server.binding.HostSID, journal)
}
