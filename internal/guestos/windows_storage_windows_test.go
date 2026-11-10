//go:build windows

package guestos

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/cage"
	"golang.org/x/sys/windows"
)

func storagePipeFixture(t *testing.T, impersonation winio.PipeImpLevel, run func(net.Conn, string)) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid := user.User.Sid.String()
	name := `\\.\pipe\Cage.StorageTest.` + uuid.NewString()
	listener, err := winio.ListenPipe(name, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;" + sid + ")"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection, err := listener.Accept()
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.Close()
		var request [1]byte
		if _, err := connection.Read(request[:]); err != nil {
			t.Error(err)
			return
		}
		run(connection, sid)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := winio.DialPipeAccessImpLevel(ctx, name, 0x120003, impersonation)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("private storage pipe test timed out")
	}
}

func TestWindowsStorageDestinationUsesCallerAccess(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "system.vhdx")
	if err := os.WriteFile(path, []byte("synthetic VHDX identity"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := protectWindowsProfile(path); err != nil {
		t.Fatal(err)
	}
	storagePipeFixture(t, winio.PipeImpLevelImpersonation, func(connection net.Conn, sid string) {
		handle, identity, err := managerOpenStorageDestination(connection, path, sid, false)
		if err != nil {
			t.Error(err)
			return
		}
		defer windows.CloseHandle(handle)
		volume, id, err := managerDiskIdentity(path)
		if err != nil || identity.Volume != volume || identity.FileID != id {
			t.Error("destination object identity changed", err)
		}
		if err := os.Rename(path, path+".replaced"); err == nil {
			t.Error("verified disk was replaceable during privileged operation")
		}
		if _, _, err := managerOpenStorageDestination(connection, path, "S-1-5-21-1-2-3-999", false); err == nil {
			t.Error("different user gained destination authority")
		}
	})
	storagePipeFixture(t, winio.PipeImpLevelIdentification, func(connection net.Conn, sid string) {
		if handle, _, err := managerOpenStorageDestination(connection, path, sid, false); err == nil {
			windows.CloseHandle(handle)
			t.Error("identity-only pipe was used for a privileged destination open")
		}
	})
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(D;;FW;;;" + user.User.Sid.String() + ")(A;;FR;;;" + user.User.Sid.String() + ")(A;;FA;;;SY)(A;;FA;;;BA)")
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
	t.Cleanup(func() { _ = protectWindowsProfile(path) })
	storagePipeFixture(t, winio.PipeImpLevelImpersonation, func(connection net.Conn, sid string) {
		if handle, _, err := managerOpenStorageDestination(connection, path, sid, false); err == nil {
			windows.CloseHandle(handle)
			t.Error("read-only caller selected a disk for privileged mutation")
		}
	})
}

func TestWindowsStoragePinsDirectoryIdentity(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "system.vhdx")
	if err := os.WriteFile(path, []byte("disk"), 0600); err != nil {
		t.Fatal(err)
	}
	closeParents, err := managerPinStorageParents(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeParents()
	if err := os.Rename(parent, parent+"-renamed"); err == nil {
		t.Fatal("ancestor could be replaced while VM storage paths were pinned")
	}
	closeParents()
	if err := os.Rename(parent, parent+"-renamed"); err != nil {
		t.Fatal("parent pin leaked after operation", err)
	}
}

func TestWindowsStorageCopiedPairingKeepsProtectedACL(t *testing.T) {
	profile, runtime, _, _ := windowsStorageFixture(t, false)
	before, err := cage.GetStorageLocation(profile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cage.MoveStorageWithRuntime(context.Background(), profile, filepath.Join(t.TempDir(), "new"), before, runtime, nil); err != nil {
		t.Fatal(err)
	}
	binding, err := LoadWindowsProfile(profile)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{binding.PairingFile, binding.NetworkPairingFile} {
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		control, _, err := sd.Control()
		if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
			t.Fatal("copied pairing is rejected by production PrivateFile", path, err)
		}
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			t.Fatal("pairing disappeared after migration")
		}
	}
}
