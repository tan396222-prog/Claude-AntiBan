package gitguard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sylvinus/agent-vm/internal/gitx"
	"github.com/sylvinus/agent-vm/internal/version"
)

// A bare repository has no name for readonlyNames to protect: a folder with
// HEAD, objects/ and refs/ is a repository to git, found by the same upward
// search from the current directory as a .git. Its config then applies, and
// some of it names commands git runs (core.pager on `git log`). The VM can
// create such a folder anywhere in a share. safe.bareRepository=explicit (git
// 2.38+) makes git use one only when --git-dir or GIT_DIR names it.

// Bare-repository states.
const (
	BareOK          = "ok"
	BareUnset       = "unset"
	BareOld         = "old" // git before 2.38, which ignores the setting
	BareNoGit       = "nogit"
	BareUnavailable = "unavailable"
)

// BareState is whether git on this machine ignores bare repositories it is
// not told about. Read from /, outside any repository: git only honours the
// setting from the system and global config.
func BareState(ctx context.Context) string {
	available, err := gitx.Available(ctx)
	if err != nil {
		return BareUnavailable
	}
	if !available {
		return BareNoGit
	}
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	out, err := gitx.Untrusted(ctx, "--version").Output()
	if err != nil {
		return BareUnavailable
	}
	v := strings.TrimPrefix(strings.TrimSpace(string(out)), "git version ")
	v, _, _ = strings.Cut(v, " ")
	// Git's release candidates (2.38.0-rc1) are not semver prereleases.
	v, _, _ = strings.Cut(v, "-")
	if !version.AtLeast(v, "2.38.0") {
		return BareOld
	}
	// gitx.Untrusted sets the value on the command line: that scope is not
	// the user's, the last of the others is.
	cmd := gitx.Untrusted(ctx, "config", "--show-scope", "--get-all", "safe.bareRepository")
	// Windows needs a volume-qualified directory. The system/user drive root
	// also keeps this global-config query outside the shared project.
	cmd.Dir = string(filepath.Separator)
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = filepath.VolumeName(home) + string(filepath.Separator)
	}
	cmd.WaitDelay = 2 * time.Second
	out, _ = cmd.Output()
	value := ""
	for _, l := range strings.Split(string(out), "\n") {
		if scope, v, ok := strings.Cut(l, "\t"); ok && scope != "command" {
			value = strings.TrimSpace(v)
		}
	}
	if value == "explicit" {
		return BareOK
	}
	return BareUnset
}

// BareHint is why the setting matters and how to set it, one paragraph per
// line.
const BareHint = `Git treats any folder with HEAD, objects/ and refs/ as a repository, even without .git, and runs commands its config names (on ` + "`git log`" + `, for one). A VM could create one in your projects, and the .git protection does not cover it.

This makes git ignore such folders unless named with --git-dir:
  git config --global safe.bareRepository explicit
`
