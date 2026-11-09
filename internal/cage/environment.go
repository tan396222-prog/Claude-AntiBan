package cage

import (
	"encoding/json"
	"github.com/sylvinus/agent-vm/internal/paths"
	"os"
	"path/filepath"
	"strings"

	"github.com/sylvinus/agent-vm/internal/vmname"
)

// EnvironmentDir is the stable VM anchor for this profile, independent of projects.
func EnvironmentDir(profile string) string {
	var saved struct {
		Directory string `json:"directory"`
	}
	if b, err := os.ReadFile(filepath.Join(profile, "environment.json")); err == nil && json.Unmarshal(b, &saved) == nil && filepath.IsAbs(saved.Directory) {
		return saved.Directory
	}
	// CLI and desktop upgrades must retain the same existing virtual disk.
	var legacy struct {
		Workspace string `json:"workspace"`
	}
	if b, err := os.ReadFile(filepath.Join(profile, "workbench.json")); err == nil && json.Unmarshal(b, &legacy) == nil && filepath.IsAbs(legacy.Workspace) {
		if _, err := os.Stat(filepath.Join(profile, "state", "lima", InstanceName(profile, legacy.Workspace), "lima.yaml")); err == nil {
			return legacy.Workspace
		}
	}
	path := filepath.Clean(profile) + "-workspace"
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}

// InstanceName is shared by the controller and each Cage CLI subprocess.
// It uses the same spelling as the CLI's AbsDir, including Windows drives.
func InstanceName(profile, workspace string) string {
	home := filepath.Join(profile, "state", "lima")
	_, err := os.Stat(filepath.Join(profile, locationFile))
	logical := err == nil
	if logical {
		if real, e := filepath.EvalSymlinks(profile); e == nil {
			home = filepath.Join(real, "state", "lima")
		}
	}
	dir := paths.Host(filepath.Clean(workspace))
	expected := vmname.HomeLimit(home, logical).Name(dir)
	if _, err := os.Stat(filepath.Join(home, expected)); err == nil {
		return expected
	}
	// A profile restored from a different path can contain an instance made
	// under a previous socket limit. Only accept a unique, valid truncation
	// of this exact workspace's name, confirmed by a persistent Lima config.
	entries, _ := os.ReadDir(home)
	var old string
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || !strings.HasSuffix(name, expected[len(expected)-9:]) || vmname.Limit(len(name)).Name(dir) != name {
			continue
		}
		if _, err := os.Stat(filepath.Join(home, name, "lima.yaml")); err != nil {
			continue
		}
		if old != "" {
			return expected
		}
		old = name
	}
	if old != "" {
		return old
	}
	return expected
}
