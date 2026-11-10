package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sylvinus/agent-vm/internal/host"
	"github.com/sylvinus/agent-vm/internal/remotelink"
)

func writeNewPrivate(path string, data []byte) error {
	if path == "" {
		return errors.New("a private output file is required")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("cannot create private output; choose a new file")
	}
	if err = host.ProtectFile(path); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(path)
		return errors.New("could not save private output")
	}
	return nil
}

func readPrivate(path string, limit int64) ([]byte, error) {
	if err := host.CheckPrivateFile(path); err != nil {
		return nil, errors.New("missing or insecure private file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open private file")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		clear(data)
		return nil, errors.New("private file exceeds limit or could not be read")
	}
	return data, nil
}

func createIdentity(dir string) (*remotelink.Identity, error) {
	// Require a new directory: do not change permissions on an existing home
	// or other user-supplied directory.
	if err := os.Mkdir(dir, 0700); err != nil {
		return nil, errors.New("identity initialization requires a new state directory in an existing parent")
	}
	if err := host.EnsurePrivateDir(dir); err != nil {
		return nil, err
	}
	seed := make([]byte, ed25519.SeedSize)
	_, _ = rand.Read(seed)
	defer clear(seed)
	if err := writeNewPrivate(filepath.Join(dir, "device.seed"), seed); err != nil {
		return nil, err
	}
	return remotelink.IdentityFromKey(ed25519.NewKeyFromSeed(seed))
}

func loadIdentity(dir string) (*remotelink.Identity, error) {
	seed, err := readPrivate(filepath.Join(dir, "device.seed"), ed25519.SeedSize)
	if err != nil {
		return nil, err
	}
	defer clear(seed)
	if len(seed) != ed25519.SeedSize {
		return nil, errors.New("invalid device identity; preserve its state directory")
	}
	return remotelink.IdentityFromKey(ed25519.NewKeyFromSeed(seed))
}

func validPin(value string) bool {
	s, ok := strings.CutPrefix(value, "sha256:")
	b, err := base64.StdEncoding.DecodeString(s)
	return ok && err == nil && len(b) == 32 && base64.StdEncoding.EncodeToString(b) == s
}

func trust(dir, pin string) error {
	if !validPin(pin) {
		return errors.New("invalid verified peer fingerprint")
	}
	identity, err := loadIdentity(dir)
	if err != nil {
		return err
	}
	if pin == identity.Pin {
		return errors.New("peer fingerprint must identify a different device")
	}
	return writeNewPrivate(filepath.Join(dir, "trusted-peer"), []byte(pin))
}

func trustedPeer(dir string) (string, error) {
	b, err := readPrivate(filepath.Join(dir, "trusted-peer"), 256)
	if err != nil || !validPin(string(b)) {
		return "", errors.New("register an independently verified peer fingerprint first")
	}
	return string(b), nil
}
