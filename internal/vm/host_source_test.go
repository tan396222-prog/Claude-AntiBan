package vm

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/hostimage"
)

func TestHostLinuxImageHasNoFallbackOrHostMounts(t *testing.T) {
	l := newTestLima(t)
	directory := t.TempDir()
	config := filepath.Join(directory, "config.json")
	data, _ := json.Marshal(cage.DefaultConfig())
	if err := os.WriteFile(config, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(cage.ConfigEnv, config)
	plan := hostimage.Plan{Host: hostimage.Identity{System: "ubuntu-22.04", Version: "22.04", Arch: "amd64"}, Version: "22.04", Format: "qcow2", Path: filepath.Join(directory, "ubuntu.img"), Digest: "sha256:" + strings.Repeat("a", 64)}
	if err := hostimage.WriteLinuxTemplate(directory, plan); err != nil {
		t.Fatal(err)
	}
	template, err := hostimage.LinuxTemplate(directory)
	if err != nil {
		t.Fatal(err)
	}
	none := []Mount{}
	if err := l.Create(t.Context(), "x", template, Settings{Mounts: &none, NoContainerd: true}, io.Discard); err != nil {
		t.Fatal(err)
	}
	instance, err := l.inspect(t.Context(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if len(instance.Config.Images) != 1 || instance.Config.Images[0].Location != plan.Path || string(instance.Config.Images[0].Digest) != plan.Digest {
		t.Fatalf("unexpected image fallback: %+v", instance.Config.Images)
	}
	if len(instance.Config.Mounts) != 0 {
		t.Fatal("host directories were mounted")
	}
	if err := hostimage.ClearLinuxTemplate(directory, plan); err != nil {
		t.Fatal(err)
	}
	if path, err := hostimage.LinuxTemplate(directory); err != nil || path != "template:debian-13" {
		t.Fatalf("selection was retained: %q %v", path, err)
	}
}
