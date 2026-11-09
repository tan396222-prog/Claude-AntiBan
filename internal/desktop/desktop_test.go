package desktop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// fake puts a shell script named name first in PATH, and nothing else that could show a dialog.
func fake(t *testing.T, name, script string) {
	t.Helper()
	dir := t.TempDir()
	if script != "" {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// /usr/bin and /bin for sh and sleep: no dialog tool lives there in the test environment.
	t.Setenv("PATH", dir+":/usr/bin:/bin")
}

type want struct{ allow, ok bool }

func TestAskLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux")
	}
	for _, tool := range []struct {
		name    string
		deny    int // the exit status of a Deny
		display string
	}{
		{"zenity", 1, "cannot open display"},
		{"kdialog", 1, "could not connect to display"},
		{"kdialog", 2, "cannot connect to X server"},
	} {
		for _, c := range []struct {
			name   string
			script string
			env    string
			want   want
		}{
			{"allow", "exit 0", ":0", want{true, true}},
			{"deny", fmt.Sprintf("exit %d", tool.deny), ":0", want{false, true}},
			{"no display tool", "echo '" + tool.display + "' >&2; exit 1", ":0", want{false, false}},
			{"crash", "kill -SEGV $$", ":0", want{false, false}},
			{"no display variable", "exit 0", "", want{false, false}},
			{"tool missing", "", ":0", want{false, false}},
		} {
			t.Run(tool.name+"/"+c.name, func(t *testing.T) {
				t.Setenv("DISPLAY", c.env)
				t.Setenv("WAYLAND_DISPLAY", "")
				fake(t, tool.name, c.script)
				allow, ok := Ask(context.Background(), "t", "m")
				if (want{allow, ok}) != c.want {
					t.Errorf("got allow=%v ok=%v, want %v", allow, ok, c.want)
				}
			})
		}
	}
}

// zenity that cannot show a dialog falls back to kdialog.
func TestAskLinuxFallback(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux")
	}
	t.Setenv("DISPLAY", ":0")
	t.Setenv("WAYLAND_DISPLAY", "")
	fake(t, "zenity", "echo 'cannot open display' >&2; exit 1")
	dir := filepath.SplitList(os.Getenv("PATH"))[0]
	if err := os.WriteFile(filepath.Join(dir, "kdialog"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if allow, ok := Ask(context.Background(), "t", "m"); !allow || !ok {
		t.Errorf("got allow=%v ok=%v", allow, ok)
	}
}

// The dialog is up when the context ends: an answer, a refusal.
func TestAskTimeout(t *testing.T) {
	var tool string
	switch runtime.GOOS {
	case "linux":
		tool = "zenity"
		t.Setenv("DISPLAY", ":0")
		t.Setenv("WAYLAND_DISPLAY", "")
	case "darwin":
		tool = "osascript"
	default:
		t.Skip("no dialog")
	}
	fake(t, tool, "exec sleep 30")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	allow, ok := Ask(ctx, "t", "m")
	if allow || !ok {
		t.Errorf("got allow=%v ok=%v", allow, ok)
	}
	if time.Since(start) > 10*time.Second {
		t.Error("did not stop with the context")
	}
}

func TestAskDarwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS")
	}
	for _, c := range []struct {
		name   string
		script string
		want   want
	}{
		{"allow", "echo 'button returned:Allow'", want{true, true}},
		{"deny button", "echo 'button returned:Deny'", want{false, true}},
		{"closed", "echo 'execution error: User canceled. (-128)' >&2; exit 1", want{false, true}},
		{"no session", "echo 'execution error: Application isn'\\''t running. (-600)' >&2; exit 1", want{false, false}},
		{"missing", "", want{false, false}},
	} {
		t.Run(c.name, func(t *testing.T) {
			fake(t, "osascript", c.script)
			if c.script == "" {
				// The real osascript lives in /usr/bin and would show a dialog.
				t.Setenv("PATH", t.TempDir())
			}
			allow, ok := Ask(context.Background(), "t", "m")
			if (want{allow, ok}) != c.want {
				t.Errorf("got allow=%v ok=%v, want %v", allow, ok, c.want)
			}
		})
	}
}
