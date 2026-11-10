package cage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/sylvinus/agent-vm/internal/paths"
	"github.com/sylvinus/agent-vm/internal/vmname"
)

type EnvironmentRecord struct {
	Directory         string `json:"directory"`
	IdentityDirectory string `json:"identity_directory,omitempty"`
}

func LoadEnvironmentRecord(profile string) (EnvironmentRecord, error) {
	var saved EnvironmentRecord
	data, err := os.ReadFile(filepath.Join(profile, "environment.json"))
	if errors.Is(err, os.ErrNotExist) {
		return saved, nil
	}
	if err != nil {
		return saved, err
	}
	if json.Unmarshal(data, &saved) != nil || !filepath.IsAbs(saved.Directory) ||
		saved.IdentityDirectory != "" && !filepath.IsAbs(saved.IdentityDirectory) {
		return saved, errors.New("环境工作目录记录无效，请保留原配置并导出诊断记录")
	}
	return saved, nil
}

// DefaultEnvironmentDir keeps generated workspaces outside the default profile,
// including independent profiles stored below that profile's directory.
func DefaultEnvironmentDir(profile string) string {
	path := filepath.Clean(profile) + "-workspace"
	if home, err := os.UserHomeDir(); err == nil {
		defaultProfile := filepath.Join(home, ".claude-cage")
		if !paths.Equal(profile, defaultProfile) && StorageContains(defaultProfile, profile) {
			if relative, err := filepath.Rel(defaultProfile, profile); err == nil {
				path = filepath.Join(defaultProfile+"-workspaces", relative)
			}
		}
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}

// EnvironmentDir is the default shared workspace, independent of the currently
// selected project. IdentityDirectory retains a legacy VM's naming input.
func EnvironmentDir(profile string) string {
	if saved, err := LoadEnvironmentRecord(profile); err == nil && saved.Directory != "" {
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
	previous := filepath.Clean(profile) + "-workspace"
	if _, err := os.Stat(filepath.Join(profile, "state", "lima", InstanceName(profile, previous), "lima.yaml")); err == nil {
		return previous
	}
	return DefaultEnvironmentDir(profile)
}

func EnvironmentIdentityDir(profile string) string {
	if saved, err := LoadEnvironmentRecord(profile); err == nil && saved.IdentityDirectory != "" {
		return saved.IdentityDirectory
	}
	return EnvironmentDir(profile)
}

// InstanceName is shared by the controller and each Cage CLI subprocess.
// It uses the same spelling as the CLI's AbsDir, including Windows drives.
func InstanceName(profile, workspace string) string {
	if saved, err := LoadEnvironmentRecord(profile); err == nil && saved.IdentityDirectory != "" &&
		paths.Equal(paths.Host(filepath.Clean(workspace)), paths.Host(filepath.Clean(saved.Directory))) {
		workspace = saved.IdentityDirectory
	}
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
