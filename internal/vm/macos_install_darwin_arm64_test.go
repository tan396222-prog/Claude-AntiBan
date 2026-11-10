//go:build darwin && arm64 && cgo && !no_vz

package vm

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Code-Hex/vz/v3"
	"github.com/lima-vm/lima/v2/pkg/fileutils"
	"github.com/lima-vm/lima/v2/pkg/limatype"
	"github.com/lima-vm/lima/v2/pkg/limayaml"
	"github.com/opencontainers/go-digest"
)

func TestMacOSInstallSourceAndMounts(t *testing.T) {
	l := &Lima{}
	dir := t.TempDir()
	t.Setenv("LIMA_HOME", filepath.Join(dir, "instances"))
	ipsw := filepath.Join(dir, "bad.ipsw")
	if err := os.WriteFile(ipsw, []byte("not a restore image"), 0o600); err != nil {
		t.Fatal(err)
	}
	shares := []Mount{{Location: dir}}
	for _, tc := range []struct {
		file string
		s    Settings
		want string
	}{
		{ipsw, Settings{Mounts: &shares}, "shared host directories"},
		{"https://localhost/mac.ipsw", Settings{}, "absolute path"},
		{ipsw, Settings{}, "Apple Virtualization"},
	} {
		if err := l.CreateMacOSFromIPSW(t.Context(), "mac", tc.file, tc.s, io.Discard); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%q: expected %q, got %v", tc.file, tc.want, err)
		}
	}
	t.Setenv("LIMA_HOME", dir)
	if err := l.CreateMacOSFromIPSW(t.Context(), "mac", ipsw, Settings{}, io.Discard); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("selected image in instance storage: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "mac")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected instance after preflight: %v", err)
	}
	if err := applyMacOSRequirements(&vz.MacOSRestoreImage{}, &Settings{}); err == nil {
		t.Fatal("accepted an unsupported native restore image")
	}
}

func TestMacOSHostSourceCompatibility(t *testing.T) {
	path := os.Getenv("CAGE_TEST_MACOS_SOURCE")
	if path == "" {
		t.Skip("set CAGE_TEST_MACOS_SOURCE to verified local restore media")
	}
	image, err := vz.LoadMacOSRestoreImageFromPath(path)
	if err != nil {
		t.Fatal(err)
	}
	settings := Settings{DiskGiB: 64}
	if err := applyMacOSRequirements(image, &settings); err != nil {
		t.Fatal(err)
	}
	t.Logf("Apple accepted the restore image, minimum CPUs=%d memoryGiB=%d", settings.CPUs, settings.MemoryGiB)
}

func TestMacOSInstallTemplateIsolation(t *testing.T) {
	expectedDigest := "sha256:" + strings.Repeat("a", 64)
	body := fmt.Sprintf(macOSInstallTemplate, `"/example.ipsw"`, expectedDigest)
	cfg, err := limayaml.Load(t.Context(), []byte(body), "macos.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := limayaml.Validate(cfg, false); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Images) != 1 || cfg.Images[0].Digest.String() != expectedDigest {
		t.Fatal("selected restore image digest was lost")
	}
	if cfg.User.Comment == nil || *cfg.User.Comment != "Cage Provisioning" || *cfg.User.Name != "cageprovision" || *cfg.User.Home != "/Users/cageprovision" {
		t.Fatal("macOS template must not inherit the host account's full name")
	}
	var options limatype.DarwinOpts
	if err := limayaml.Convert(cfg.OsOpts[limatype.DARWIN], &options, "osOpts.Darwin"); err != nil || options.SuppressFirstLoginSetup == nil || !*options.SuppressFirstLoginSetup {
		t.Fatal("new guest did not prepare first-login settings", err)
	}
	if len(cfg.Mounts) != 0 || len(cfg.Networks) != 0 || *cfg.SSH.LoadDotSSHPubKeys || *cfg.SSH.ForwardAgent || *cfg.SSH.ForwardX11 {
		t.Fatal("unexpected host sharing in macOS template")
	}
	if len(cfg.PortForwards) != 1 || !cfg.PortForwards[0].Ignore || cfg.PortForwards[0].GuestPortRange != [2]int{1, 65535} || cfg.PortForwards[0].Proto != "any" || *cfg.PortForwards[0].GuestIPMustBeZero {
		t.Fatal("default guest port forwarding is not disabled")
	}
}

func TestSelectedIPSWRejectsReplacementAtSamePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "selected.ipsw")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	selected, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	pin, err := fileutils.RestoreImageDigest(t.Context(), path, selected)
	if err != nil || pin != digest.FromString("original") {
		t.Fatalf("unexpected media pin: %s, %v", pin, err)
	}
	replacement := filepath.Join(dir, "renamed")
	if err := os.WriteFile(replacement, []byte("replaced"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacement, selected.ModTime(), selected.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	if _, err := fileutils.RestoreImageDigest(t.Context(), path, selected); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("accepted another inode with the original size and mtime: %v", err)
	}
	file := limatype.File{Location: path, Arch: limatype.AARCH64, Digest: pin}
	if _, err := fileutils.DownloadFile(t.Context(), filepath.Join(dir, "image"), file, true, "restore image", limatype.AARCH64, nil); err == nil || !strings.Contains(err.Error(), "expected digest") {
		t.Fatalf("Lima accepted replaced installation media: %v", err)
	}
}
