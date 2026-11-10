//go:build darwin && arm64

package workbench

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/guestos"
)

// Opt-in only: use the isolated release QA VM, never a personal profile. The
// source account and its key remain unchanged; guest passwords are restored.
func TestMacSharedPasswordLive(t *testing.T) {
	root := os.Getenv("CAGE_MACOS_SHARED_ACCOUNT_QA_ROOT")
	if root == "" {
		t.Skip("requires an owned, running macOS release QA VM")
	}
	raw, err := os.ReadFile(filepath.Join(root, "ownership.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ownership struct {
		Purpose  string `json:"purpose"`
		Existing bool   `json:"existing_user_data"`
	}
	if json.Unmarshal(raw, &ownership) != nil || ownership.Existing || ownership.Purpose != "Seven release issue fixes and native guest acceptance" {
		t.Fatal("not the isolated release QA environment")
	}
	profile := filepath.Join(root, "profile")
	original, err := loadLocalAccount(profile)
	if err != nil {
		t.Fatal(err)
	}
	defer original.lock()
	password, err := os.ReadFile(filepath.Join(root, "qa-account-password"))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(password)
	if err = original.unlock(string(password)); err != nil {
		t.Fatal(err)
	}
	accountBefore, err := os.ReadFile(original.path)
	if err != nil {
		t.Fatal(err)
	}
	install, err := guestos.LoadInstallation(profile)
	if err != nil || install == nil {
		t.Fatal("missing QA installation")
	}
	bound, err := guestos.LoadMacOSProfile(profile)
	if err != nil || bound == nil {
		t.Fatal("missing QA guest binding")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	previous, err := original.savedMacGuestPassword(ctx, install.ID)
	if err != nil || previous == "" {
		t.Fatal("missing encrypted QA credential")
	}
	target := `Shared test-'"\$! 2026`
	fixture, err := loadLocalAccount(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.lock()
	if err = fixture.create(bound.User, target, ""); err != nil {
		t.Fatal(err)
	}
	if err = fixture.prepareMacAccount(bound.User, target, map[string]macGuestCredential{install.ID.String(): {Username: bound.User, Password: previous}}); err != nil {
		t.Fatal(err)
	}
	kit := t.TempDir()
	if err = os.Mkdir(filepath.Join(kit, "guest-os"), 0700); err != nil {
		t.Fatal(err)
	}
	helper, err := os.ReadFile(os.Getenv("CAGE_MACOS_SHARED_ACCOUNT_HELPER"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(kit, "guest-os", "cage-finish-initial-setup"), helper, 0700); err != nil {
		t.Fatal(err)
	}
	s := &Server{profile: profile, account: fixture, macosGuest: bound, exe: filepath.Join(kit, "cage")}
	sentinel := ".cage-shared-account-" + uuid.NewString()
	cmd := s.macOSSSHCommand(ctx, "-T", "cage-macos", "--", `umask 077; printf preserved > "$HOME/`+sentinel+`"`)
	if err = cmd.Run(); err != nil {
		t.Fatal("cannot prepare isolated guest sentinel", err)
	}
	defer func() {
		_ = s.macOSSSHCommand(context.Background(), "-T", "cage-macos", "--", `rm -f "$HOME/`+sentinel+`"`).Run()
	}()
	defer func() {
		restore, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := s.applyMacGuestPassword(restore, install.ID, target, previous); err != nil {
			t.Errorf("restore original QA guest credential: %v", err)
		}
	}()
	if err = s.syncMacGuestPassword(ctx); err != nil {
		t.Fatal(err)
	}
	// Both ordinary login and the temporary authorization user accept Cage's
	// password. SSH still authenticates by key, so this checks password login too.
	for _, user := range []string{bound.User, "cagesetup"} {
		cmd = s.macOSSSHCommand(ctx, "-T", "cage-macos", "--", "/usr/bin/dscl /Local/Default -authonly "+user)
		cmd.Stdin = strings.NewReader(target + "\n")
		if err = cmd.Run(); err != nil {
			t.Fatal("guest account did not accept shared password for", user, err)
		}
	}
	cmd = s.macOSSSHCommand(ctx, "-T", "cage-macos", "--", "/usr/bin/dscl /Local/Default -authonly "+bound.User)
	cmd.Stdin = strings.NewReader(previous + "\n")
	if cmd.Run() == nil {
		t.Fatal("ordinary account still accepted its previous password")
	}
	provisionArgs, err := guestos.MacOSSSHArgs(profile, "cageprovision")
	if err != nil {
		t.Fatal(err)
	}
	check := exec.CommandContext(ctx, sshExecutable(), append(provisionArgs, "-T", "cage-macos", "--", `sudo -S -p '' /usr/bin/shasum -a 256 /etc/kcpassword < /Users/cageprovision/password`)...)
	actual, err := check.Output()
	if err != nil {
		t.Fatal("cannot verify system automatic login record", err)
	}
	encoded := append([]byte(target), 0)
	for len(encoded)%12 != 0 {
		encoded = append(encoded, 0)
	}
	key := []byte{0x7d, 0x89, 0x52, 0x23, 0xd2, 0xbc, 0xdd, 0xea, 0xa3, 0xb9, 0x1f}
	for i := range encoded {
		encoded[i] ^= key[i%len(key)]
	}
	expected := sha256.Sum256(encoded)
	clear(encoded)
	if !strings.HasPrefix(string(actual), hex.EncodeToString(expected[:])+" ") {
		t.Fatal("system automatic login did not receive the new password")
	}
	// Replay the durable old/target pair as if the first reply was lost.
	if err = s.applyMacGuestPassword(ctx, install.ID, previous, target); err != nil {
		t.Fatal("idempotent replay:", err)
	}
	cmd = s.macOSSSHCommand(ctx, "-T", "cage-macos", "--", `test "$(cat "$HOME/`+sentinel+`")" = preserved && ! id -Gn | /usr/bin/grep -qw admin`)
	if err = cmd.Run(); err != nil {
		t.Fatal("guest data or ordinary account privilege changed", err)
	}
	accountAfter, err := os.ReadFile(original.path)
	if err != nil || !bytes.Equal(accountBefore, accountAfter) {
		t.Fatal("source QA account changed")
	}
	t.Log("Shared password accepted by ordinary and setup accounts; old password rejected; automatic login and replay passed; source account and guest sentinel retained")
}
