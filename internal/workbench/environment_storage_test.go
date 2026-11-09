package workbench

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/sylvinus/agent-vm/internal/cage"
)

func TestStorageFirstSelectionAndMigrationThroughWorkbench(t *testing.T) {
	s := testServer(t)
	const endpoint = "/api/environment/storage"
	query := func() cage.StorageLocation {
		t.Helper()
		r := request(s, "GET", endpoint, s.origin, "", true)
		var loc cage.StorageLocation
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &loc) != nil {
			t.Fatalf("%d %s", r.Code, r.Body.String())
		}
		return loc
	}
	previous := query()
	if previous.HasEnvironment {
		t.Fatal("fresh profile reported an environment")
	}
	target := filepath.Join(t.TempDir(), "Cage 空间")
	write := func(target string, prior cage.StorageLocation) Job {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"directory": target, "previous": prior.Directory, "migrate": prior.HasEnvironment})
		r := request(s, "POST", endpoint, s.origin, string(body), true)
		if r.Code != http.StatusOK {
			t.Fatal(r.Code, r.Body.String())
		}
		return awaitJobIdle(t, &s.jobs)
	}
	if job := write(target, previous); job.Error != "" {
		t.Fatal(job)
	}
	location := query()
	real, err := filepath.EvalSymlinks(target)
	if err != nil || location.Directory != real || !location.Custom {
		t.Fatal(location, real, err)
	}
	for _, path := range []string{target, filepath.Dir(target), filepath.Join(target, "state")} {
		if _, err := validateWorkspace(path, s.profile); err == nil {
			t.Fatal("environment storage exposed as project", path)
		}
	}
	project := t.TempDir()
	if err := s.rememberSharedProject(project); err != nil {
		t.Fatal(err)
	}
	if job := write(filepath.Join(project, "private-disks"), location); job.Error == "" {
		t.Fatal("saved project accepted as environment storage")
	}
	staleTarget := filepath.Join(t.TempDir(), "old-window")
	if job := write(staleTarget, previous); job.Error == "" {
		t.Fatal("stale window redirected the active environment", job)
	}
	if _, err := os.Stat(staleTarget); !os.IsNotExist(err) {
		t.Fatal("stale request created a destination", err)
	}
	// A disk added between showing the form and submitting it must now require
	// the migration confirmation. Exercise the subsequent cold move through
	// the same authenticated HTTP operation used by the settings page.
	relativeDisk := filepath.Join("state", "lima", "fixture", "diffdisk")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(target, relativeDisk)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, relativeDisk), []byte("isolated VM disk"), 0600); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(t.TempDir(), "Cage 环境")
	if job := write(second, location); job.Error == "" {
		t.Fatal("unconfirmed migration was accepted")
	}
	from := query()
	if !from.HasEnvironment {
		t.Fatal("disk was not discovered")
	}
	if job := write(second, from); job.Error != "" {
		t.Fatal(job)
	}
	moved := query()
	if !moved.HasEnvironment || moved.Backup != from.Directory {
		t.Fatal(moved)
	}
	for _, root := range []string{moved.Backup, moved.Directory} {
		data, err := os.ReadFile(filepath.Join(root, relativeDisk))
		if err != nil || string(data) != "isolated VM disk" {
			t.Fatal(root, err)
		}
	}
}

func TestStorageChangeIsLocalAndRequiresUnlockedSession(t *testing.T) {
	s := testServer(t)
	const endpoint = "/api/environment/storage"
	s.remote = newRemoteGateway(s.profile)
	defer s.remote.manager.Close()
	s.remote.setRemote(true)
	if r := request(s, "POST", endpoint, s.origin, "{}", true); r.Code != http.StatusForbidden {
		t.Fatal("remote connection routed a storage request", r.Code)
	}
	s.remote.setRemote(false)
	s.account.lock()
	if r := request(s, "POST", endpoint, s.origin, "{}", true); r.Code != http.StatusLocked {
		t.Fatal("locked user could change storage", r.Code)
	}
}
