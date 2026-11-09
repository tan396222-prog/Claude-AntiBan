package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/cli"
	"github.com/sylvinus/agent-vm/internal/vmname"
)

func TestControllerNamesMatchCLIForIndependentProfiles(t *testing.T) {
	base := os.TempDir()
	if filepath.Separator == '/' {
		base = "/tmp"
	}
	root, err := os.MkdirTemp(base, "vn")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, strings.Repeat("long-directory-", 6))
	if err = os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	saved, err := json.Marshal(map[string]string{"directory": workspace})
	if err != nil {
		t.Fatal(err)
	}
	a := &Server{profile: filepath.Join(root, "a")}
	b := &Server{profile: filepath.Join(root, "another-profile")}
	cliName := func(s *Server) string {
		t.Helper()
		t.Setenv(cage.ConfigEnv, filepath.Join(s.profile, "config.json"))
		t.Setenv("AGENT_VM_STATE_DIR", filepath.Join(s.profile, "state"))
		t.Setenv("AGENT_VM_LIMA_HOME", filepath.Join(s.profile, "state", "lima"))
		var out, stderr bytes.Buffer
		if n := cli.Main([]string{"name", workspace}, cli.IO{Stdout: &out, Stderr: &stderr}); n != 0 {
			t.Fatalf("CLI name: %s", stderr.String())
		}
		return strings.TrimSpace(out.String())
	}
	for _, s := range []*Server{a, b} {
		if err = os.Mkdir(s.profile, 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(s.profile, "environment.json"), saved, 0600); err != nil {
			t.Fatal(err)
		}
	}
	an, bn := cliName(a), cliName(b)
	if an == bn || len(bn) >= len(an) || len(an) >= 76 {
		t.Fatalf("test requires different truncations: %q, %q", an, bn)
	}
	var wg sync.WaitGroup
	for s, want := range map[*Server]string{a: an, b: bn} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				if got := s.sshTarget(); got != "lima-"+want {
					t.Errorf("SSH target %s; CLI name %s", got, want)
					return
				}
				if got := filepath.Base(s.instanceDir()); got != want {
					t.Errorf("instance directory %s; CLI name %s", got, want)
					return
				}
			}
		}()
	}
	wg.Wait()
	loc, err := cage.GetStorageLocation(b.profile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cage.MoveStorage(context.Background(), b.profile, filepath.Join(root, strings.Repeat("data-", 10)), loc, nil); err != nil {
		t.Fatal(err)
	}
	if got := cliName(b); got != bn || b.sshTarget() != "lima-"+bn {
		t.Fatalf("storage migration changed VM name: %s -> %s", bn, got)
	}
	// Preserve a stopped VM restored from an earlier, shorter socket path.
	old := vmname.Limit(len(bn) + 4).Name(workspace)
	dir := filepath.Join(b.profile, "state", "lima", old)
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "lima.yaml"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := cliName(b); got != old || b.sshTarget() != "lima-"+old {
		t.Fatalf("existing disk %s not reused; CLI picked %s", old, got)
	}
}
