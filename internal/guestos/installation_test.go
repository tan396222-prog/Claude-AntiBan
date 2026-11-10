package guestos

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallationKeepsChoiceAndRefusesToClearVM(t *testing.T) {
	p := t.TempDir()
	first := Installation{Version: 1, System: MacOS, ID: uuid.New()}
	if err := SaveInstallation(p, first); err != nil {
		t.Fatal(err)
	}
	if err := SaveInstallation(p, first); err != nil {
		t.Fatal(err)
	}
	other := first
	other.ID = uuid.New()
	if err := SaveInstallation(p, other); err == nil {
		t.Fatal("overwrote an active installation")
	}
	if err := ClearUnstartedInstallation(p, other.ID); err == nil {
		t.Fatal("stale reset changed selection")
	}
	disk := filepath.Join(p, "state", "lima", "mac")
	if err := os.MkdirAll(disk, 0700); err != nil {
		t.Fatal(err)
	}
	if err := ClearUnstartedInstallation(p, first.ID); err == nil {
		t.Fatal("forgot a partially installed VM")
	}
	got, err := LoadInstallation(p)
	if err != nil || got == nil || *got != first {
		t.Fatalf("changed original selection: %v %v", got, err)
	}
	if err := os.Remove(disk); err != nil {
		t.Fatal(err)
	}
	if err := ClearUnstartedInstallation(p, first.ID); err != nil {
		t.Fatal(err)
	}
	got, err = LoadInstallation(p)
	if err != nil || got != nil {
		t.Fatal("failed to clear media-only choice")
	}
}

func TestWindowsInstallRequiresMediaDigestAndEditorAgreement(t *testing.T) {
	i := Installation{Version: 1, System: Windows11, ID: uuid.New(), Media: filepath.Join(t.TempDir(), "system.iso"), SHA256: strings.Repeat("a", 64)}
	if err := i.Validate(); err == nil {
		t.Fatal("accepted without the editor agreement")
	}
	i.EditorTerms = true
	if err := i.Validate(); err != nil {
		t.Fatal(err)
	}
	i.SHA256 = "bad"
	if err := i.Validate(); err == nil {
		t.Fatal("accepted an incomplete ISO digest")
	}
}

func TestMacSerialIdentityIsUnambiguous(t *testing.T) {
	newKey := func() []byte {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		key, err := ssh.NewPublicKey(pub)
		if err != nil {
			t.Fatal(err)
		}
		return ssh.MarshalAuthorizedKey(key)
	}
	key := newKey()
	prefix := "CAGE-GUEST-HOST-KEY "
	line := prefix + strings.TrimSpace(string(key)) + " guest\r\n"
	got, err := parseInstallHostKey(strings.NewReader("startup\n" + line + line))
	if err != nil || !bytes.Equal(key, got) {
		t.Fatalf("valid serial identity: %v", err)
	}
	for _, body := range []string{"", "other " + line, line + prefix + "bad", line + prefix + string(newKey())} {
		if _, err := parseInstallHostKey(strings.NewReader(body)); err == nil {
			t.Fatal("accepted missing or inconsistent identity")
		}
	}
}

func TestInitialMacPasswordCanBeEnteredAtTheLoginWindow(t *testing.T) {
	for _, value := range []string{"Mix @#?9", strings.Repeat("!", 256)} {
		if !ValidMacOSInstallPassword(value) {
			t.Fatal("rejected an enterable password")
		}
	}
	for _, value := range []string{"short", "中文输入示例密码", "abcde\tfgh", strings.Repeat("a", 257)} {
		if ValidMacOSInstallPassword(value) {
			t.Fatal("accepted an unsupported password")
		}
	}
}
