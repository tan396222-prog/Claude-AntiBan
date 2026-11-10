package hostimage_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sylvinus/agent-vm/internal/hostimage"
	"github.com/sylvinus/agent-vm/internal/limaembed"
)

func TestUbuntu2204PinnedImageAndPersistentTemplate(t *testing.T) {
	template, err := limaembed.ReadImageTemplate("ubuntu-22.04")
	if err != nil {
		t.Fatal(err)
	}
	identity := hostimage.Identity{System: "ubuntu-22.04", Version: "22.04", Arch: "amd64"}
	state := t.TempDir()
	plan, err := hostimage.Resolve(context.Background(), identity, state, t.TempDir(), template, nil)
	if err != nil || !strings.Contains(plan.URL, "/jammy/release-20261106/") || len(plan.Digest) != 71 {
		t.Fatalf("source: %+v %v", plan, err)
	}
	plan.Path = filepath.Join(state, "ubuntu.img")
	if err := hostimage.WriteLinuxTemplate(state, plan); err != nil {
		t.Fatal(err)
	}
	saved, err := hostimage.Load(filepath.Join(state, "guest-media"))
	if err != nil || saved != plan {
		t.Fatalf("persistent media: %+v %v", saved, err)
	}
	path, err := hostimage.LinuxTemplate(state)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	var config struct{ Images []map[string]string }
	if err != nil || json.Unmarshal(data, &config) != nil || len(config.Images) != 1 || config.Images[0]["location"] != plan.Path {
		t.Fatalf("template changed source or added fallbacks: %s %v", data, err)
	}
	plan.URL = "https://cloud-images.ubuntu.com.evil.test/ubuntu.img"
	if err := plan.Validate(); err == nil {
		t.Fatal("accepted an untrusted Ubuntu image source")
	}
}

func TestUbuntuChoiceNeverFallsBackAfterMetadataLoss(t *testing.T) {
	for _, removed := range []string{"host-system.yaml", "host-system-source.json"} {
		state := t.TempDir()
		template, _ := limaembed.ReadImageTemplate("ubuntu-22.04")
		plan, err := hostimage.Resolve(t.Context(), hostimage.Identity{System: "ubuntu-22.04", Version: "22.04", Arch: "arm64"}, state, state, template, nil)
		if err != nil {
			t.Fatal(err)
		}
		plan.Path = filepath.Join(state, "ubuntu.img")
		if err := hostimage.WriteLinuxTemplate(state, plan); err != nil {
			t.Fatal(err)
		}
		if system, err := hostimage.LinuxSystem(state); err != nil || system != "ubuntu-22.04" {
			t.Fatalf("identity: %q %v", system, err)
		}
		if err := os.Remove(filepath.Join(state, "guest-media", removed)); err != nil {
			t.Fatal(err)
		}
		if _, err := hostimage.LinuxTemplate(state); err == nil {
			t.Fatalf("silently fell back after losing %s", removed)
		}
	}
}
