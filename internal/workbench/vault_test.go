package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVaultEncryptionLockReopenPasswordChangeAndCorruption(t *testing.T) {
	a := newTestAccount(t)
	ctx := context.Background()
	err := a.withVault(ctx, true, func(db *vaultData) error {
		return db.save(vaultEntry{Origin: "https://mail.example.com/login", Username: "private@example.com", Password: "secret-mail-password"})
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(a.path), "password-vault.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, plain := range []string{"private@example.com", "secret-mail-password", "mail.example.com"} {
		if bytes.Contains(original, []byte(plain)) {
			t.Fatal("plaintext persisted")
		}
	}
	if err = a.changePassword("initial-test-password", "changed-account-password"); err != nil {
		t.Fatal(err)
	}
	oldLease := a.vaultLease
	a.lock()
	if err = a.withVault(ctx, false, func(*vaultData) error { t.Fatal("locked callback executed"); return nil }); err == nil {
		t.Fatal("locked read accepted")
	}
	reopened, err := loadLocalAccount(filepath.Dir(a.path))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.lock()
	if err = reopened.unlock("changed-account-password"); err != nil {
		t.Fatal(err)
	}
	if reopened.vaultLease == oldLease {
		t.Fatal("unlock reused browser lease")
	}
	err = reopened.withVault(ctx, false, func(db *vaultData) error {
		if len(db.Entries) != 1 || db.Entries[0].Password != "secret-mail-password" || db.Entries[0].Origin != "https://mail.example.com" {
			t.Fatalf("data lost after password change/reopen: count %d", len(db.Entries))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var envelope vaultEnvelope
	if json.Unmarshal(original, &envelope) != nil {
		t.Fatal("invalid envelope")
	}
	envelope.Data[0] ^= 1
	corrupted, _ := json.Marshal(envelope)
	if err = os.WriteFile(path, corrupted, 0600); err != nil {
		t.Fatal(err)
	}
	if err = reopened.withVault(ctx, true, func(db *vaultData) error { return db.save(vaultEntry{Origin: "https://new.example", Password: "new"}) }); err == nil {
		t.Fatal("corrupt vault overwritten")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(corrupted, after) {
		t.Fatal("corrupt vault changed")
	}
}

func TestVaultAPIReauthenticationAndIsolation(t *testing.T) {
	s := testServer(t)
	s.account = newTestAccount(t)
	save := `{"action":"save","entry":{"origin":"https://mail.example.com","username":"alice@example.com","password":"mail-secret"}}`
	w := request(s, "POST", "/api/vault", "https://evil.example", save, true)
	if w.Code != 403 {
		t.Fatalf("cross origin accepted: %d", w.Code)
	}
	w = request(s, "POST", "/api/vault", s.origin, save, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(s, "GET", "/api/vault", s.origin, "", true)
	if w.Code != 200 || strings.Contains(w.Body.String(), "mail-secret") {
		t.Fatal("list leaks password", w.Code)
	}
	var db vaultData
	if json.Unmarshal(w.Body.Bytes(), &db) != nil || len(db.Entries) != 1 {
		t.Fatal("missing saved entry")
	}
	reveal := func(password string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]string{"action": "reveal", "id": db.Entries[0].ID, "account_password": password})
		return request(s, "POST", "/api/vault", s.origin, string(b), true)
	}
	if w = reveal("wrong"); w.Code == 200 || strings.Contains(w.Body.String(), "mail-secret") {
		t.Fatal("wrong password revealed")
	}
	s.account.nextAttempt = time.Time{}
	// testServer uses its own account password; match the fixture definition.
	if w = reveal("initial-test-password"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "mail-secret") {
		t.Fatal("verified reveal missing")
	}
	s.account.lock()
	for _, path := range []string{"/api/vault", "/api/vault/browser"} {
		if w = request(s, "POST", path, s.origin, save, true); w.Code != http.StatusLocked {
			t.Fatal("locked endpoint accepted", path, w.Code)
		}
	}
}

func TestVaultBrowserNeverExposesCandidatePasswordAndUsesStoredOrigin(t *testing.T) {
	s := testServer(t)
	s.ready, s.credentials.Token = true, "vault-guest-test"
	var sent vaultBrowserInput
	guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/vault" || r.Header.Get("Authorization") != "Bearer vault-guest-test" {
			t.Error("wrong guest transport")
		}
		if json.NewDecoder(r.Body).Decode(&sent) != nil {
			t.Error("invalid body")
		}
		switch sent.Action {
		case "status":
			jsonReply(w, vaultBrowserState{Available: true, Origin: "https://mail.example.com", Document: "doc", Candidates: []vaultCandidate{{ID: "candidate", Origin: "https://mail.example.com", Username: "alice", Password: "site-secret"}}})
		case "candidate":
			jsonReply(w, vaultCandidate{ID: "candidate", Origin: "https://mail.example.com", Username: "alice", Password: "site-secret"})
		default:
			jsonReply(w, map[string]bool{"ok": true})
		}
	}))
	defer guest.Close()
	s.browserControl = localBrowserClient(t, s.lifetimeContext(), guest)
	w := request(s, "POST", "/api/vault/browser", s.origin, `{"action":"status","tab":"tab"}`, true)
	if w.Code != 200 || strings.Contains(w.Body.String(), "site-secret") {
		t.Fatal("candidate leaked", w.Code)
	}
	w = request(s, "POST", "/api/vault/browser", s.origin, `{"action":"save","candidate":"candidate"}`, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(s, "GET", "/api/vault", s.origin, "", true)
	var db vaultData
	if json.Unmarshal(w.Body.Bytes(), &db) != nil || len(db.Entries) != 1 {
		t.Fatal("candidate not saved")
	}
	b, _ := json.Marshal(map[string]string{"action": "fill", "id": db.Entries[0].ID, "tab": "tab", "document": "doc", "origin": "https://evil.example", "password": "injected", "lease": "forged"})
	w = request(s, "POST", "/api/vault/browser", s.origin, string(b), true)
	if w.Code != 200 || sent.Origin != "https://mail.example.com" || sent.Password != "site-secret" || sent.Lease != s.account.vaultLease {
		t.Fatal("fill did not use stored credential")
	}
	w = request(s, "POST", "/api/vault/browser", s.origin, `{"action":"status","tab":"tab"}`, true)
	var state vaultBrowserState
	if json.Unmarshal(w.Body.Bytes(), &state) != nil || len(state.Candidates) != 0 {
		t.Fatal("identical credential prompted again")
	}
}

func TestVaultOriginAndDuplicateUpdate(t *testing.T) {
	db := vaultData{}
	for _, raw := range []string{"http://mail.example.com", "https://user:pass@mail.example.com", "file:///test", "https://"} {
		if db.save(vaultEntry{Origin: raw, Password: "secret"}) == nil {
			t.Fatal("invalid origin accepted", raw)
		}
	}
	if err := db.save(vaultEntry{Origin: "https://MAIL.example.com:443/login", Username: "alice", Password: "old"}); err != nil {
		t.Fatal(err)
	}
	if db.save(vaultEntry{Origin: "https://mail.example.com", Username: "alice", Password: "duplicate"}) == nil {
		t.Fatal("duplicate accepted")
	}
	if err := db.save(vaultEntry{ID: db.Entries[0].ID, Origin: "https://mail.example.com", Username: "alice"}); err != nil {
		t.Fatal(err)
	}
	if len(db.Entries) != 1 || db.Entries[0].Password != "old" {
		t.Fatal("blank edit lost existing password")
	}
	if err := db.save(vaultEntry{Origin: "https://mail.example.com", Username: "bob", Password: "other"}); err != nil {
		t.Fatal(err)
	}
}
