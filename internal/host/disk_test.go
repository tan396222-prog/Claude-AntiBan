package host

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A VM folder not made yet uses its nearest existing parent.
func TestWarnDiskSpace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	missing := filepath.Join(t.TempDir(), "not", "yet")
	var b bytes.Buffer
	WarnDiskSpace(missing, 1<<30, &b)
	if !strings.HasPrefix(b.String(), "Warning: ~") {
		t.Errorf("no warning: %q", b.String())
	}
	b.Reset()
	WarnDiskSpace(missing, 0, &b)
	WarnDiskSpace(missing, 1, &b)
	if b.Len() != 0 {
		t.Errorf("warned: %q", b.String())
	}
}

// Native QA passes a profile-side junction to a second volume with different
// free space. The missing child models Lima's first launch before setup.
func TestDiskSpaceBeforeVMDirectoryExists(t *testing.T) {
	alias, target := os.Getenv("CAGE_TEST_DISK_SPACE_ALIAS"), os.Getenv("CAGE_TEST_DISK_SPACE_VOLUME")
	if alias == "" || target == "" {
		t.Skip("requires a test link to a second volume")
	}
	missing := filepath.Join(alias, "cage-disk-qa-absent", "lima")
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("expected missing QA child", err)
	}
	want, ok := FreeGiB(target)
	if !ok {
		t.Fatal("cannot read physical volume")
	}
	got, ok := FreeGiB(missing)
	if !ok || got != want {
		t.Fatalf("logical free = %d, expected physical free = %d (ok=%v)", got, want, ok)
	}
	var warning bytes.Buffer
	WarnDiskSpace(missing, want, &warning)
	if warning.Len() != 0 {
		t.Fatal("spurious low space warning", warning.String())
	}
	WarnDiskSpace(missing, want+1, &warning)
	if !strings.Contains(warning.String(), fmt.Sprintf("~%d GiB free", want)) {
		t.Fatalf("wrong preflight warning %q", warning.String())
	}
	t.Logf("first-run space check: %d GiB available on target", got)
}
