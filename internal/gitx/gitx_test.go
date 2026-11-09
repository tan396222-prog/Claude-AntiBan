package gitx

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

func TestAppleGitDetectionDoesNotRunGitLauncher(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	t.Setenv("DEVELOPER_DIR", "")
	dir := t.TempDir()
	selector := filepath.Join(dir, "xcode-select")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(selector, []byte("#!/bin/sh\n"+body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	write("echo 'xcode-select: error: unable to get active developer directory. Use xcode-select --switch.' >&2\nexit 2\n")
	if available, err := appleGitAvailable(context.Background(), selector); available || err != nil {
		t.Fatalf("missing tools: %v %v", available, err)
	}
	t.Setenv("DEVELOPER_DIR", "/invalid-override")
	if available, err := appleGitAvailable(context.Background(), selector); available || err == nil {
		t.Fatalf("invalid override treated as absent: %v %v", available, err)
	}
	t.Setenv("DEVELOPER_DIR", "")
	write("echo 'unexpected failure' >&2\nexit 2\n")
	if available, err := appleGitAvailable(context.Background(), selector); available || err == nil {
		t.Fatalf("unknown failure treated as absent: %v %v", available, err)
	}
	developer := filepath.Join(dir, "Developer")
	write("printf '%s\\n' " + strconv.Quote(developer) + "\n")
	if available, err := appleGitAvailable(context.Background(), selector); available || err == nil {
		t.Fatalf("broken installation accepted: %v %v", available, err)
	}
	bin := filepath.Join(developer, "usr", "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if available, err := appleGitAvailable(context.Background(), selector); !available || err != nil {
		t.Fatalf("installed tools: %v %v", available, err)
	}
}
