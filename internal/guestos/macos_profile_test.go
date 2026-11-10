package guestos

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"
)

func TestPreparedMacOSBindingRejectsChangedKeyAndDisk(t *testing.T) {
	profile := t.TempDir()
	dir := filepath.Join(profile, "state", "lima", "mac")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	identifier := filepath.Join(dir, "vz-identifier")
	if err := os.WriteFile(identifier, []byte("one trial machine"), 0600); err != nil {
		t.Fatal(err)
	}
	key := func() []byte {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		p, err := ssh.NewPublicKey(pub)
		if err != nil {
			t.Fatal(err)
		}
		return ssh.MarshalAuthorizedKey(p)
	}
	pub := key()
	if err := BindMacOSProfile(profile, "dev", pub); err != nil {
		t.Fatal(err)
	}
	if err := BindMacOSProfile(profile, "dev", pub); err != nil {
		t.Fatal(err)
	}
	if err := BindMacOSProfile(profile, "dev", key()); err == nil {
		t.Error("changed host key accepted")
	}
	if err := BindMacOSProfile(profile, "root", pub); err == nil {
		t.Error("root login accepted")
	}
	if m, err := LoadMacOSProfile(profile); err != nil || m.User != "dev" {
		t.Fatal(m, err)
	}
	if err := os.WriteFile(identifier, []byte("replaced guest"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMacOSProfile(profile); err == nil {
		t.Error("replacement machine accepted")
	}
}

func TestMacFirstSetupCanOnlySealItsNewGuest(t *testing.T) {
	profile := t.TempDir()
	dir := filepath.Join(profile, "state", "lima", "mac")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vz-identifier"), []byte("new-mac"), 0600); err != nil {
		t.Fatal(err)
	}
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	hostKey := ssh.MarshalAuthorizedKey(key)
	id := uuid.New()
	if err = BindNewMacOSProfile(profile, "cageuser", hostKey, id); err == nil {
		t.Fatal("accepted missing install record")
	}
	if err = SaveInstallation(profile, Installation{Version: 1, System: MacOS, ID: id}); err != nil {
		t.Fatal(err)
	}
	if err = BindNewMacOSProfile(profile, "cageuser", hostKey, id); err != nil {
		t.Fatal(err)
	}
	if err = FinishMacOSSetup(profile, uuid.NewString()); err == nil {
		t.Fatal("sealed unrelated install")
	}
	bound, err := LoadMacOSProfile(profile)
	if err != nil || bound.SetupID != id.String() {
		t.Fatal("lost pending setup")
	}
	if err = FinishMacOSSetup(profile, id.String()); err != nil {
		t.Fatal(err)
	}
	if err = BindNewMacOSProfile(profile, "cageuser", hostKey, id); err == nil {
		t.Fatal("a retried installer reopened setup")
	}
	bound, err = LoadMacOSProfile(profile)
	if err != nil || bound.SetupID != "" {
		t.Fatal("setup still open")
	}
}
