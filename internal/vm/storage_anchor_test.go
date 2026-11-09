package vm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lima-vm/lima/v2/pkg/limatype/dirnames"
	"github.com/sylvinus/agent-vm/internal/cage"
)

func TestStorageMoveKeepsLimaIdentityAndSocketPaths(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(root, "profile")
	home := filepath.Join(profile, "state", "lima")
	if err := os.MkdirAll(filepath.Join(home, "guest"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIMA_HOME", home)
	t.Setenv("CAGE_LOGICAL_LIMA_HOME", "")
	before, err := dirnames.InstanceDir("guest")
	if err != nil {
		t.Fatal(err)
	}
	limit := MaxName(home)
	expected, err := cage.GetStorageLocation(profile)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, strings.Repeat("physical-path-", 4))
	if _, err := cage.MoveStorage(context.Background(), profile, target, expected, nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAGE_LOGICAL_LIMA_HOME", home)
	after, err := dirnames.InstanceDir("guest")
	if err != nil {
		t.Fatal(err)
	}
	if after != before || MaxName(home) != limit {
		t.Fatalf("migration changed the instance path/name limit: before %s/%d, after %s/%d", before, limit, after, MaxName(home))
	}
}
