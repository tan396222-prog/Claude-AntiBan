package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestMacSharedAccountHTTPRegistration(t *testing.T) {
	s := testServer(t)
	if !s.macAccountHost() {
		t.Skip("requires a Mac host")
	}
	a, err := loadLocalAccount(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.lock()
	s.account = a
	w := request(s, "POST", "/api/account/create", s.origin, `{"username":"unsupported name","password":"fixture-password","confirm_password":"fixture-password"}`, true)
	if w.Code != http.StatusBadRequest || a.view().Exists {
		t.Fatalf("invalid registration: %d", w.Code)
	}
	w = request(s, "POST", "/api/account/create", s.origin, `{"username":"http_qa","password":"fixture-password","confirm_password":"fixture-password"}`, true)
	var view accountView
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &view) != nil || !view.MacOSCredentialsReady || view.Username != "http_qa" {
		t.Fatalf("registration did not prepare shared credentials: %d %s", w.Code, w.Body.String())
	}
	guest, err := a.macGuestCredentials(context.Background(), uuid.New())
	if err != nil || guest.Username != view.Username || guest.Password != "fixture-password" {
		t.Fatal("installer did not receive registered account", err)
	}
}

func newSharedMacAccount(t *testing.T) *localAccount {
	t.Helper()
	a, err := loadLocalAccount(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = a.createAccount("shihao", "initial-test-password", "", true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.lock)
	return a
}

func TestMacGuestCredentialEncryptionResumeAndIsolation(t *testing.T) {
	a := newSharedMacAccount(t)
	ctx := context.Background()
	id := uuid.New()
	guest, err := a.macGuestCredentials(ctx, id)
	if err != nil || guest.Username != "shihao" || guest.Password != "initial-test-password" {
		t.Fatalf("shared account: %#v %v", guest, err)
	}
	raw, _ := os.ReadFile(a.path)
	if bytes.Contains(raw, []byte(guest.Password)) {
		t.Fatal("plaintext password persisted")
	}
	a.lock()
	if err = a.unlock(guest.Password); err != nil {
		t.Fatal(err)
	}
	resumed, err := a.macGuestCredentials(ctx, id)
	if err != nil || resumed != guest {
		t.Fatal("retry changed installation credentials", err)
	}
	other, err := a.macGuestCredentials(ctx, uuid.New())
	if err != nil || other != guest {
		t.Fatal("new installation did not reuse Cage credentials", err)
	}
	s := &Server{account: a}
	w := httptest.NewRecorder()
	s.vaultList(w, httptest.NewRequest("GET", "/api/vault", nil))
	if bytes.Contains(w.Body.Bytes(), []byte(guest.Password)) || bytes.Contains(w.Body.Bytes(), []byte("MacLogin")) {
		t.Fatal("website vault exposed shared credentials")
	}
	if err := a.updateProfile("another", ""); err == nil {
		t.Fatal("bound username was renamed")
	}
	a.lock()
	if _, err = a.savedMacGuestPassword(ctx, id); err == nil {
		t.Fatal("locked account decrypted guest credentials")
	}
}

func TestMacGuestPasswordChangeOfflineAndInterruptedSync(t *testing.T) {
	a := newSharedMacAccount(t)
	ctx := context.Background()
	id := uuid.New()
	_, err := a.macGuestCredentials(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.changePassword("initial-test-password", "replacement-test-password"); err != nil {
		t.Fatal(err)
	}
	if !a.view().MacOSPasswordPending {
		t.Fatal("offline guest update disappeared")
	}
	// A second edit must not discard a password that may already be in the guest.
	if err = a.changePassword("replacement-test-password", "third-test-password"); err == nil {
		t.Fatal("pending target was replaced")
	}
	path := a.path
	a.lock()
	a, err = loadLocalAccount(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	defer a.lock()
	if err = a.unlock("replacement-test-password"); err != nil {
		t.Fatal(err)
	}
	old, _ := a.savedMacGuestPassword(ctx, id)
	if old != "initial-test-password" {
		t.Fatal("offline guest lost its confirmed password")
	}
	applied := "initial-test-password"
	if err = a.syncMacPassword(ctx, id, "different-user", func(context.Context, string, string) error { t.Fatal("wrong guest account changed"); return nil }); err == nil {
		t.Fatal("identity mismatch accepted")
	}
	// Simulate loss of the reply after the guest password changes.
	err = a.syncMacPassword(ctx, id, "shihao", func(_ context.Context, old, target string) error {
		if old != applied {
			t.Fatal("missing original credential")
		}
		applied = target
		return errors.New("lost reply")
	})
	if err == nil || !a.view().MacOSPasswordPending {
		t.Fatal("partial update was reported complete")
	}
	err = a.syncMacPassword(ctx, id, "shihao", func(_ context.Context, old, target string) error {
		if old != "initial-test-password" || target != applied {
			t.Fatal("retry lost original or target credential")
		}
		return nil
	})
	if err != nil || a.view().MacOSPasswordPending {
		t.Fatal("retry did not finish", err)
	}
	saved, _ := a.savedMacGuestPassword(ctx, id)
	if saved != applied {
		t.Fatal("authorization helper received stale password")
	}
	if err = a.changePassword(applied, "third-test-password"); err != nil {
		t.Fatal("completed sync blocked next change", err)
	}
}

func TestMacGuestPasswordFailedSaveAndAcknowledgement(t *testing.T) {
	a := newSharedMacAccount(t)
	ctx := context.Background()
	id := uuid.New()
	if _, err := a.macGuestCredentials(ctx, id); err != nil {
		t.Fatal(err)
	}
	path := a.path
	a.path = t.TempDir()
	if err := a.changePassword("initial-test-password", "replacement-test-password"); err == nil {
		t.Fatal("failed account write accepted")
	}
	a.path = path
	if a.view().MacOSPasswordPending {
		t.Fatal("failed account write published new guest target")
	}
	if err := a.changePassword("initial-test-password", "replacement-test-password"); err != nil {
		t.Fatal(err)
	}
	err := a.syncMacPassword(ctx, id, "shihao", func(context.Context, string, string) error { a.path = t.TempDir(); return nil })
	a.path = path
	if err == nil || !a.view().MacOSPasswordPending {
		t.Fatal("failed acknowledgement lost retry")
	}
	if err = a.syncMacPassword(ctx, id, "shihao", func(context.Context, string, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestMacGuestLegacyCredentialAndAccountAdoption(t *testing.T) {
	a := newTestAccount(t)
	ctx := context.Background()
	id := uuid.New()
	if err := a.withVault(ctx, true, func(db *vaultData) error {
		db.GuestPasswords = map[string]string{id.String(): "old-guest-password"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	old, err := a.macGuestCredentials(ctx, id)
	if err != nil || old.Username != "cage" || old.Password != "old-guest-password" {
		t.Fatal("legacy installation credential changed", err)
	}
	before, _ := a.desktopKey()
	if err = a.prepareMacAccount("cage", "initial-test-password", map[string]macGuestCredential{id.String(): old}); err != nil {
		t.Fatal(err)
	}
	after, _ := a.desktopKey()
	defer clear(before)
	defer clear(after)
	if !bytes.Equal(before, after) {
		t.Fatal("adoption replaced personal-data encryption key")
	}
	if err = a.syncMacPassword(ctx, id, "cage", func(_ context.Context, old, target string) error {
		if old != "old-guest-password" || target != "initial-test-password" {
			t.Fatal("legacy migration lost either password")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	resumed, err := a.macGuestCredentials(ctx, id)
	if err != nil || resumed.Password != "initial-test-password" {
		t.Fatal("legacy vault overrode synced password", err)
	}
}

func TestMacAccountRegistrationAndTrustedAdoption(t *testing.T) {
	for _, name := range []string{"root", "cagesetup", "cageprovision", "Guest", "测试", "name with spaces"} {
		if validMacAccount(name, "fixture-password") == nil {
			t.Fatalf("accepted unsupported username %q", name)
		}
	}
	a, err := loadLocalAccount(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.lock()
	devices := t.TempDir()
	if err = a.trustedLogin("create", "", devices); err != nil {
		t.Fatal(err)
	}
	before, _ := a.desktopKey()
	defer clear(before)
	if err = a.prepareMacAccount("shihao", "new-test-password", nil); err != nil {
		t.Fatal(err)
	}
	after, _ := a.desktopKey()
	defer clear(after)
	if !bytes.Equal(before, after) || a.view().TrustedLocal {
		t.Fatal("trusted adoption lost data key or retained passwordless account")
	}
	a.lock()
	if err = a.unlock("new-test-password"); err != nil {
		t.Fatal(err)
	}
	if validMacAccount("shihao", "密码不可用于macOS") == nil {
		t.Fatal("accepted unenterable guest password")
	}
}
