package guestos

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"regexp"

	"golang.org/x/crypto/ssh"
)

// MacOSProfile binds one prepared local VZ guest to its ordinary development account.
type MacOSProfile struct {
	Version   int    `json:"version"`
	Instance  string `json:"instance"`
	User      string `json:"user"`
	MachineID string `json:"machineID"`
	SetupID   string `json:"initial_setup,omitempty"`
}

var macUser = regexp.MustCompile(`^[a-z][a-z0-9_]{0,30}$`)

// New accounts must not collide with macOS or Cage's provisioning identities.
func ValidMacOSAccountName(name string) bool {
	if !macUser.MatchString(name) {
		return false
	}
	switch name {
	case "root", "daemon", "nobody", "guest", "cageprovision", "cagesetup":
		return false
	}
	return true
}

func macMachineID(profile string) (string, error) {
	path := filepath.Join(profile, "state", "lima", "mac", "vz-identifier")
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > 4096 {
		return "", errors.New("invalid Apple virtual machine identifier")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func LoadMacOSProfile(profile string) (*MacOSProfile, error) {
	path := filepath.Join(profile, "macos-guest.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return nil, errors.New("invalid macOS guest profile")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m MacOSProfile
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m.Version != 1 || m.Instance != "mac" || !macUser.MatchString(m.User) || m.User == "root" {
		return nil, errors.New("invalid macOS guest profile or login")
	}
	if m.SetupID != "" && !validSetupID(m.SetupID) {
		return nil, errors.New("invalid first setup identity")
	}
	id, err := macMachineID(profile)
	if err != nil {
		return nil, err
	}
	if id != m.MachineID {
		return nil, errors.New("the macOS guest differs from the machine bound to this profile")
	}
	return &m, nil
}

// BindMacOSProfile takes an independently obtained guest host public key. Do
// not discover this key by scanning an unverified localhost SSH port.
func BindMacOSProfile(profile, user string, hostKey []byte) error {
	return bindMacOSProfile(profile, user, hostKey, "")
}

// BindNewMacOSProfile is called only by the fresh, resumable installer.
func BindNewMacOSProfile(profile, user string, hostKey []byte, id uuid.UUID) error {
	install, err := LoadInstallation(profile)
	if err != nil {
		return err
	}
	if install == nil || id == uuid.Nil || install.ID != id || install.System != MacOS {
		return errors.New("首次设置与当前安装记录不匹配")
	}
	return bindMacOSProfile(profile, user, hostKey, id.String())
}

func validSetupID(id string) bool {
	value, err := uuid.Parse(id)
	return err == nil && value != uuid.Nil && value.String() == id
}

func bindMacOSProfile(profile, user string, hostKey []byte, setupID string) error {
	if !macUser.MatchString(user) || user == "root" {
		return errors.New("supply the short name of the ordinary macOS guest account")
	}
	key, _, options, rest, err := ssh.ParseAuthorizedKey(hostKey)
	if err != nil || len(options) != 0 || len(bytes.TrimSpace(rest)) != 0 || key.Type() != ssh.KeyAlgoED25519 {
		return errors.New("expected one Ed25519 guest SSH host public key")
	}
	id, err := macMachineID(profile)
	if err != nil {
		return err
	}
	m := MacOSProfile{Version: 1, Instance: "mac", User: user, MachineID: id, SetupID: setupID}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if current, err := LoadMacOSProfile(profile); err != nil {
		return err
	} else if current != nil && *current != m {
		return errors.New("this profile is already bound to another macOS guest account")
	}
	if err := publishBinding(profile, "macos-known-hosts", append([]byte("cage-macos "), ssh.MarshalAuthorizedKey(key)...)); err != nil {
		return err
	}
	return publishBinding(profile, "macos-guest.json", append(data, '\n'))
}

func publishBinding(profile, name string, data []byte) error {
	temp, err := os.CreateTemp(profile, ".guest-binding-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	_, writeErr := temp.Write(data)
	if err := errors.Join(writeErr, temp.Close()); err != nil {
		return err
	}
	path := filepath.Join(profile, name)
	if err := os.Link(temp.Name(), path); !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(data)) {
		return fmt.Errorf("unexpected existing %s", name)
	}
	prior, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, prior) {
		return fmt.Errorf("refusing to replace existing %s", name)
	}
	return nil
}

// PinMacOSInstallHostKey persists only the host key, without enabling the workbench.
func PinMacOSInstallHostKey(profile string) error {
	key, err := MacOSInstallHostKey(profile)
	if err != nil {
		return err
	}
	return publishBinding(profile, "macos-known-hosts", append([]byte("cage-macos "), key...))
}

// FinishMacOSSetup removes the installation identity from the existing binding.
// Binding publication is create-only, so retrying an installer cannot reopen it.
func FinishMacOSSetup(profile, setupID string) error {
	bound, err := LoadMacOSProfile(profile)
	if err != nil {
		return err
	}
	if bound == nil || !validSetupID(setupID) || bound.SetupID != setupID {
		return errors.New("首次设置已关闭或安装实例已变更")
	}
	bound.SetupID = ""
	data, err := json.Marshal(bound)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(profile, ".finish-guest-setup-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(append(data, '\n'))
	if err = errors.Join(writeErr, f.Sync(), f.Close()); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), filepath.Join(profile, "macos-guest.json")); err != nil {
		return err
	}
	dir, err := os.Open(profile)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
