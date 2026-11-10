package workbench

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sylvinus/agent-vm/internal/host"
	"golang.org/x/sys/windows"
)

func TestDesktopLocalAdministratorOwnedLock(t *testing.T) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("requires an elevated Windows token")
	}
	for _, held := range []bool{false, true} {
		name := "idle"
		if held {
			name = "live"
		}
		t.Run(name, func(t *testing.T) {
			profile := t.TempDir()
			if err := host.EnsurePrivateDir(profile); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(profile, "control.lock")
			lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			admin, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
			if err != nil {
				t.Fatal(err)
			}
			if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, admin, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
			if err := host.CheckPrivateFile(path); err == nil {
				t.Fatal("fixture is not an old group-owned lock")
			}
			if held {
				if err := lockController(lock); err != nil {
					t.Fatal(err)
				}
				defer unlockController(lock)
			}
			exe, _ := os.Executable()
			st, err := NewDesktopLocalController(filepath.Join(profile, "config.json"), exe).Status(context.Background())
			if err != nil || st.ServiceRunning != held || st.Known == held {
				t.Fatalf("lock ownership confused with liveness: %+v %v", st, err)
			}
			if !held {
				if err := host.CheckPrivateFile(path); err != nil {
					t.Fatalf("idle lock was not migrated to private current-user ownership: %v", err)
				}
			}
		})
	}
}
