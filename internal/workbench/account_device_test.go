package workbench

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestDeviceLoginRespectsPersistentManualLock(t *testing.T) {
	a := newTestAccount(t)
	automatic, err := a.enrollDevice("automatic")
	if err != nil {
		t.Fatal(err)
	}
	biometric, err := a.enrollDevice("biometric")
	if err != nil {
		t.Fatal(err)
	}
	original, _ := a.desktopKey()
	defer clear(original)
	data, _ := os.ReadFile(a.path)
	if bytes.Contains(data, []byte(automatic.Secret)) || bytes.Contains(data, original) {
		t.Fatal("device secret stored outside keychain")
	}
	a.lock() // App exit clears memory but allows the authorized automatic reopen.
	reopened, err := loadLocalAccount(filepath.Dir(a.path))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.lock()
	if err = reopened.unlockDevice("automatic", automatic); err != nil {
		t.Fatal(err)
	}
	if err = reopened.lockAccess(false); err != nil {
		t.Fatal(err)
	}
	locked, err := loadLocalAccount(filepath.Dir(a.path))
	if err != nil {
		t.Fatal(err)
	}
	defer locked.lock()
	if err = locked.unlockDevice("automatic", automatic); err == nil {
		t.Fatal("reopening bypassed manual lock")
	}
	if err = locked.unlockDevice("biometric", automatic); err == nil {
		t.Fatal("automatic token accepted as biometric proof")
	}
	if err = locked.unlockDevice("biometric", biometric); err != nil {
		t.Fatal(err)
	}
	after, _ := locked.desktopKey()
	defer clear(after)
	if !bytes.Equal(after, original) {
		t.Fatal("device unlock changed guest key")
	}
	if locked.view().RequireAuthentication {
		t.Fatal("authenticated unlock did not clear manual lock")
	}
	if err = locked.revokeDevice("biometric"); err != nil {
		t.Fatal(err)
	}
	locked.lock()
	if err = locked.unlockDevice("biometric", biometric); err == nil {
		t.Fatal("revoked credential accepted")
	}
	if err = locked.unlockDevice("automatic", automatic); err != nil {
		t.Fatal(err)
	}
	if err = locked.lockAccess(true); err != nil {
		t.Fatal(err)
	}
	if err = locked.unlockDevice("automatic", automatic); err == nil {
		t.Fatal("logout retained automatic authority")
	}
	if err = locked.unlock("initial-test-password"); err != nil {
		t.Fatal("password fallback lost", err)
	}
	if len(locked.view().Devices) != 0 {
		t.Fatal("logout retained device enrollment")
	}
}

func TestDeviceEnrollmentRequiresAccountAndAuthenticToken(t *testing.T) {
	a := newTestAccount(t)
	credential, err := a.enrollDevice("automatic")
	if err != nil {
		t.Fatal(err)
	}
	a.lock()
	if _, err = a.enrollDevice("biometric"); err == nil {
		t.Fatal("locked account issued a device token")
	}
	tampered := credential
	tampered.Secret = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if err = a.unlockDevice("automatic", tampered); err == nil {
		t.Fatal("forged device credential accepted")
	}
	if err = a.unlockDevice("automatic", credential); err != nil {
		t.Fatal(err)
	}
	replacement, err := a.enrollDevice("automatic")
	if err != nil {
		t.Fatal(err)
	}
	a.lock()
	if err = a.unlockDevice("automatic", credential); err == nil {
		t.Fatal("replaced device credential accepted")
	}
	if err = a.unlockDevice("automatic", replacement); err != nil {
		t.Fatal(err)
	}
}
