//go:build darwin && arm64 && cgo && !no_vz

package vm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Code-Hex/vz/v3"
	"github.com/lima-vm/lima/v2/pkg/fileutils"
	"github.com/lima-vm/lima/v2/pkg/limatype"
	"github.com/lima-vm/lima/v2/pkg/limatype/dirnames"
)

// CheckMacOSGuest verifies a prepared instance before binding or starting a
// native workbench. Project access goes through a separate confined service.
func (l *Lima) CheckMacOSGuest(ctx context.Context, name string) error {
	inst, err := l.inspect(ctx, name)
	if err != nil {
		return err
	}
	c := inst.Config
	if c == nil || c.OS == nil || *c.OS != limatype.DARWIN || c.VMType == nil || *c.VMType != limatype.VZ ||
		len(c.Mounts) != 0 || len(c.AdditionalDisks) != 0 || c.Video.Display == nil || *c.Video.Display != "offscreen" {
		return errors.New("expected a headless macOS VZ guest with its own system disk and no host shares")
	}
	for _, p := range c.PortForwards {
		if !p.Ignore {
			return errors.New("macOS guest has an unmanaged forwarded port")
		}
	}
	return NetworkConfined(c)
}

// MatchMacOSInstallSource permits retrying an interrupted restore only with
// the same pinned IPSW. An existing disk is left to Lima's restore transaction.
func (l *Lima) MatchMacOSInstallSource(ctx context.Context, name, image string) error {
	if err := l.CheckMacOSGuest(ctx, name); err != nil {
		return err
	}
	inst, err := l.inspect(ctx, name)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(image) || len(inst.Config.Images) != 1 {
		return errors.New("an interrupted installation requires its original local IPSW")
	}
	image, err = filepath.EvalSymlinks(image)
	if err != nil {
		return err
	}
	original := inst.Config.Images[0]
	if original.Location != image || original.Digest == "" {
		return errors.New("the existing macOS installation uses different restore media")
	}
	info, err := os.Stat(image)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("the restore image must be a regular file")
	}
	digest, err := fileutils.RestoreImageDigest(ctx, image, info)
	if err != nil {
		return err
	}
	if string(original.Digest) != string(digest) {
		return errors.New("the existing macOS installation is pinned to a different IPSW digest")
	}
	return nil
}

// CreateMacOSFromIPSW creates the stopped VM. Lima restores the local image
// into its private disk at first start, so the source must remain available.
func (l *Lima) CreateMacOSFromIPSW(ctx context.Context, name, imagePath string, settings Settings, log io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if settings.Mounts != nil && len(*settings.Mounts) != 0 {
		return errors.New("macOS installation does not accept shared host directories")
	}
	if err := dirnames.ValidateInstName(name); err != nil {
		return err
	}
	if !filepath.IsAbs(imagePath) || !strings.EqualFold(filepath.Ext(imagePath), ".ipsw") {
		return errors.New("select a local .ipsw file using its absolute path")
	}
	image, err := filepath.EvalSymlinks(imagePath)
	if err != nil {
		return err
	}
	info, err := os.Stat(image)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("the selected IPSW must be a regular file")
	}
	home, err := dirnames.LimaDir()
	if err != nil {
		return err
	}
	// Compare actual directories, including on case-insensitive volumes and a
	// profile whose Lima home is a symlink to another volume.
	store, err := os.Stat(home)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if store != nil {
		for dir := filepath.Dir(image); ; dir = filepath.Dir(dir) {
			ancestor, err := os.Stat(dir)
			if err != nil {
				return err
			}
			if os.SameFile(ancestor, store) {
				return errors.New("keep the source IPSW outside the Lima instance storage directory")
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	restore, err := vz.LoadMacOSRestoreImageFromPath(image)
	if err != nil {
		return fmt.Errorf("Apple Virtualization could not read the selected restore image: %w", err)
	}
	if err := applyMacOSRequirements(restore, &settings); err != nil {
		return err
	}
	selectedDigest, err := fileutils.RestoreImageDigest(ctx, image, info)
	if err != nil {
		return fmt.Errorf("pinning the selected macOS restore image: %w", err)
	}
	template, err := os.CreateTemp("", "cage-macos-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(template.Name())
	location, err := json.Marshal(image)
	if err == nil {
		_, err = fmt.Fprintf(template, macOSInstallTemplate, location, selectedDigest)
	}
	closeErr := template.Close()
	if err := errors.Join(err, closeErr); err != nil {
		return err
	}
	settings.NoContainerd = true
	if err := l.Create(ctx, name, template.Name(), settings, log); err != nil {
		return err
	}
	requirements, err := macOSImageResourceRequirements(restore)
	if err != nil {
		return err
	}
	return saveMacOSResourceRequirements(filepath.Join(home, name), requirements)
}

func applyMacOSRequirements(restore *vz.MacOSRestoreImage, settings *Settings) error {
	requirements, err := macOSImageResourceRequirements(restore)
	if err != nil {
		return err
	}
	minCPU, minGiB := requirements.CPUs, requirements.MemoryGiB
	if settings.CPUs == 0 {
		settings.CPUs = minCPU
	}
	if settings.MemoryGiB == 0 {
		settings.MemoryGiB = minGiB
	}
	if settings.CPUs < minCPU || settings.MemoryGiB < minGiB {
		return fmt.Errorf("this macOS restore image requires at least %d CPUs and %d GiB RAM", minCPU, minGiB)
	}
	return nil
}

const macOSInstallTemplate = `os: Darwin
arch: aarch64
vmType: vz
images:
  - location: %s
    arch: aarch64
    digest: %s
user:
  name: cageprovision
  comment: Cage Provisioning
  home: /Users/cageprovision
  uid: 501
osOpts:
  Darwin:
    suppressFirstLoginSetup: true
provision:
  - mode: system
    script: |
      #!/bin/sh
      set -eu
      [ "$(/usr/bin/uname -sm)" = "Darwin arm64" ]
      case "$(/usr/sbin/sysctl -n hw.model)" in VirtualMac*,*) ;; *) exit 1 ;; esac
      /usr/bin/ssh-keygen -A >/dev/null
      # This serial port is attached by VZ to this specific private instance.
      # Publish only the public key. SSH is pinned before provisioning begins.
      /bin/echo "CAGE-GUEST-HOST-KEY $(/bin/cat /etc/ssh/ssh_host_ed25519_key.pub)" >/dev/tty.virtio
video:
  display: offscreen
mounts: []
networks: []
ssh:
  loadDotSSHPubKeys: false
  forwardAgent: false
  forwardX11: false
portForwards:
  - guestIP: 0.0.0.0
    guestIPMustBeZero: false
    guestPortRange: [1, 65535]
    proto: any
    ignore: true
`
