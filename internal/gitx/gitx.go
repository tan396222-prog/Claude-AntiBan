// Package gitx runs git on this machine in folders the VM can write.
package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Untrusted is git for agent-vm's own calls in a folder the VM can write.
// The VM may have planted a repository there: a bare one, whatever the
// user's safe.bareRepository, or a .git where it cannot be protected.
// Command-line config is trusted where a repository's own is not:
// safe.bareRepository=explicit refuses the bare one, core.fsmonitor=false
// stops the command a repository's config names from running on commands
// that read the index, and --no-pager the pager its config would start.
func Untrusted(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-pager", "-c", "safe.bareRepository=explicit", "-c", "core.fsmonitor=false"}, args...)...)
	if available, err := Available(ctx); err != nil {
		cmd.Err = err
	} else if !available {
		cmd.Err = exec.ErrNotFound
	}
	return cmd
}

// Have reports whether git is installed.
func Have() bool {
	available, err := Available(context.Background())
	// An unreadable installation is not proof of absence. Scans must report
	// the failed command; they must not silently skip host Git protections.
	return available || err != nil
}

// Available checks Apple's launcher without invoking it: running /usr/bin/git
// on a Mac without developer tools can unexpectedly open the system installer.
func Available(ctx context.Context) (bool, error) {
	path, err := exec.LookPath("git")
	if errors.Is(err, exec.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false, err
	}
	if runtime.GOOS != "darwin" || resolved != "/usr/bin/git" {
		return true, nil
	}
	return appleGitAvailable(ctx, "/usr/bin/xcode-select")
}

func appleGitAvailable(ctx context.Context, selector string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, selector, "--print-path")
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		// Only the documented no-active-tools diagnostic is absence. A broken
		// DEVELOPER_DIR override, timeout, or other failure stays an error.
		if os.Getenv("DEVELOPER_DIR") == "" && errors.As(err, &exit) &&
			strings.Contains(string(exit.Stderr), "unable to get active developer directory") {
			return false, nil
		}
		return false, fmt.Errorf("cannot determine whether Apple Git is installed")
	}
	developer := strings.TrimSpace(string(out))
	if !filepath.IsAbs(developer) {
		return false, fmt.Errorf("invalid developer tools directory")
	}
	info, err := os.Stat(filepath.Join(developer, "usr", "bin", "git"))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return false, fmt.Errorf("Apple Git is unavailable in the selected developer tools")
	}
	return true, nil
}
