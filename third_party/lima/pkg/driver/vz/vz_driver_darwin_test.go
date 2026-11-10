//go:build darwin && !no_vz

// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package vz

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	applevz "github.com/Code-Hex/vz/v3"
	"github.com/lima-vm/lima/v2/pkg/limatype"
	"github.com/lima-vm/lima/v2/pkg/limatype/filenames"
	"github.com/opencontainers/go-digest"
)

func TestOffscreenGuestDisplay(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("macOS guests require Apple Silicon")
	}
	inst := &limatype.Instance{Config: &limatype.LimaYAML{OS: new(limatype.DARWIN)}}
	inst.Config.Video.Display = new("offscreen")
	boot, err := newMacOSBootLoader()
	assert.NilError(t, err)
	config, err := applevz.NewVirtualMachineConfiguration(boot, 2, 4<<30)
	assert.NilError(t, err)
	assert.NilError(t, attachDisplay(inst, config))

	driver := &LimaVzDriver{Instance: inst}
	assert.Equal(t, driver.Info(t.Context()).Features.CanRunGUI, false)
	assert.ErrorContains(t, driver.RunGUI(t.Context()), "RunGUI is not supported")
}

func TestWaitForStopped(t *testing.T) {
	t.Run("returns nil after the VM has stopped", func(t *testing.T) {
		l := &LimaVzDriver{machine: &virtualMachineWrapper{}}
		go func() {
			l.machine.mu.Lock()
			l.machine.stopped = true
			l.machine.mu.Unlock()
		}()
		assert.NilError(t, l.waitForStopped(10*time.Second))
	})

	t.Run("returns an error when the VM is still running", func(t *testing.T) {
		l := &LimaVzDriver{machine: &virtualMachineWrapper{}}
		assert.ErrorContains(t, l.waitForStopped(600*time.Millisecond), "timeout")
	})
}

func TestInterruptedMacOSRestorePreservesMediaAndPublishedDisk(t *testing.T) {
	dir := t.TempDir()
	disk := filepath.Join(dir, filenames.Disk)
	media := filepath.Join(dir, filenames.Image)
	assert.NilError(t, os.WriteFile(media, []byte("source"), 0o600))
	failure := errors.New("interrupted")
	err := restoreMacOSDisk(disk, func(stage string) error {
		assert.NilError(t, os.WriteFile(stage, []byte("partial"), 0o600))
		_, statErr := os.Stat(disk)
		assert.Assert(t, errors.Is(statErr, os.ErrNotExist))
		return failure
	})
	assert.Assert(t, errors.Is(err, failure))
	_, err = os.Stat(disk)
	assert.Assert(t, errors.Is(err, os.ErrNotExist))
	_, err = os.Stat(media)
	assert.NilError(t, err)
	// A killed installer cannot execute its deferred cleanup.
	assert.NilError(t, os.WriteFile(disk+".installing", []byte("partial"), 0o600))
	assert.NilError(t, restoreMacOSDisk(disk, func(stage string) error {
		_, err := os.Stat(stage)
		assert.Assert(t, errors.Is(err, os.ErrNotExist))
		return os.WriteFile(stage, []byte("ready"), 0o600)
	}))
	data, err := os.ReadFile(disk)
	assert.NilError(t, err)
	assert.Equal(t, string(data), "ready")
	_, err = os.Stat(media)
	assert.Assert(t, errors.Is(err, os.ErrNotExist))
	assert.NilError(t, restoreMacOSDisk(disk, func(string) error { return errors.New("attempted to reinstall an existing guest disk") }))
}

func TestRemovePIDFile(t *testing.T) {
	dir := t.TempDir()
	l := &LimaVzDriver{
		Instance: &limatype.Instance{
			Dir:    dir,
			Config: &limatype.LimaYAML{VMType: new(limatype.VZ)},
		},
	}
	pidFile := filepath.Join(dir, filenames.PIDFile(limatype.VZ))
	assert.NilError(t, os.WriteFile(pidFile, []byte("42\n"), 0o644))

	l.removePIDFile()
	_, err := os.Stat(pidFile)
	assert.Assert(t, errors.Is(err, os.ErrNotExist))

	// Removing a PID file that does not exist is not an error.
	l.removePIDFile()
}

func TestPinnedMacOSRestoreChecksPrivateInstallerInput(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(t.TempDir(), "selected.ipsw")
	image := filepath.Join(dir, filenames.Image)
	target := filepath.Join(dir, filenames.ImageIPSW)
	for _, path := range []string{source, image} {
		assert.NilError(t, os.WriteFile(path, []byte("changed"), 0o600))
	}
	cfg := &limatype.LimaYAML{Images: []limatype.Image{{File: limatype.File{Location: source, Digest: digest.FromString("selected")}}}}
	inst := &limatype.Instance{Dir: dir, Config: cfg}
	_, err := verifiedMacOSIPSW(t.Context(), inst)
	assert.ErrorContains(t, err, "digest mismatch")
	// The original source returns. An interrupted attempt's private image
	// must be repaired before the native installer can use it.
	assert.NilError(t, os.WriteFile(source, []byte("selected"), 0o600))
	got, err := verifiedMacOSIPSW(t.Context(), inst)
	assert.NilError(t, err)
	assert.Equal(t, got, target)
	assert.NilError(t, os.WriteFile(source, []byte("changed again"), 0o600))
	data, err := os.ReadFile(target)
	assert.NilError(t, err)
	assert.Equal(t, string(data), "selected")
	// A restart can reuse the verified private copy without the original.
	assert.NilError(t, os.Remove(source))
	_, err = verifiedMacOSIPSW(t.Context(), inst)
	assert.NilError(t, err)
}
