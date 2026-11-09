// Package state is agent-vm's folder, ~/.agent-vm (AGENT_VM_STATE_DIR): the
// user's files (env, volumes, runtime.sh, setup.sh) and agent-vm's markers,
// one file per VM and fact, named as 0.2 named them.
package state

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sylvinus/agent-vm/internal/paths"
)

// Dir is the state folder.
type Dir string

// Default is $AGENT_VM_STATE_DIR, made absolute so that the checks that keep
// shares away from it compare real locations, or ~/.agent-vm, spelled as
// paths.Host does.
func Default() (Dir, error) {
	if d := os.Getenv("AGENT_VM_STATE_DIR"); d != "" {
		abs, err := filepath.Abs(d)
		if err != nil {
			return "", err
		}
		return Dir(paths.Host(abs)), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return Dir(paths.Host(filepath.Join(home, ".agent-vm"))), nil
}

// Path is name in the folder.
func (d Dir) Path(name string) string {
	return paths.Host(filepath.Join(string(d), name))
}

// The markers of a VM.
const (
	VersionOf    = ".agent-vm-version-"     // the base's build time it was cloned from
	BuiltByOf    = ".agent-vm-built-by-"    // the agent-vm version that built that base
	SSHFSOf      = ".agent-vm-sshfs-"       // 0.1 migration done
	TermOf       = ".agent-vm-term-"        // the $TERM whose terminfo it has
	FileMountsOf = ".agent-vm-file-mounts-" // single files of ~/.agent-vm/volumes
	MountsOf     = ".agent-vm-mounts-"      // 0.2's record of its shares: only removed now
	CacheOf      = ".agent-vm-sshfs-cache-" // 0.2's record of sshfs's cache: only removed now
	ScratchOf    = ".agent-vm-scratch-"     // a --scratch run, with its pid
	PolicyOf     = ".agent-vm-policy-"      // fingerprint of the network and guarded policies it booted with
)

// The base template's markers. BaseVersion, written last by setup, says it
// can be cloned.
const (
	BaseVersion = ".agent-vm-base-version"
	BaseBuiltBy = ".agent-vm-base-built-by"
)

// Marker is the path of marker kind (one of the ...Of) for vm.
func (d Dir) Marker(kind, vm string) string {
	return d.Path(kind + vm)
}

// Read is the content of name in the folder, without its trailing newline,
// "" when there is none.
func (d Dir) Read(name string) string {
	b, err := os.ReadFile(d.Path(name))
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(string(b), "\n")
}

// Write replaces name in the folder, made if need be, through a temporary
// file renamed over it: a reader sees the old content or the new, never part
// of it. The temporary name does not start like a marker's.
func (d Dir) Write(name string, data []byte) error {
	if err := os.MkdirAll(string(d), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(string(d), ".tmp-"+name+"-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	if err := errors.Join(werr, tmp.Chmod(0o644), tmp.Close()); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), d.Path(name)); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// ReadMarker is Read for a marker of vm.
func (d Dir) ReadMarker(kind, vm string) string {
	return d.Read(kind + vm)
}

var markers = []string{VersionOf, BuiltByOf, SSHFSOf, TermOf, FileMountsOf, MountsOf, CacheOf, ScratchOf, PolicyOf}

// Rename moves what is known of the VM from to its new name to: its
// markers, and its staged single files (its shares are set again before it
// next boots, from where they now are).
func (d Dir) Rename(from, to string) error {
	var last error
	for _, kind := range markers {
		if err := os.Rename(d.Marker(kind, from), d.Marker(kind, to)); err != nil && !os.IsNotExist(err) {
			last = err
		}
	}
	if err := os.Rename(d.Path(filepath.Join("file-mounts", from)), d.Path(filepath.Join("file-mounts", to))); err != nil && !os.IsNotExist(err) {
		last = err
	}
	return last
}

// Forget removes what is known of vm, once deleted. Deleting the base
// template retires the marker that says it can be cloned.
func (d Dir) Forget(vm, template string) error {
	var last error
	for _, kind := range markers {
		if err := os.Remove(d.Marker(kind, vm)); err != nil && !os.IsNotExist(err) {
			last = err
		}
	}
	if err := os.RemoveAll(d.Path(filepath.Join("file-mounts", vm))); err != nil {
		last = err
	}
	if vm == template {
		for _, f := range []string{BaseVersion, BaseBuiltBy} {
			if err := os.Remove(d.Path(f)); err != nil && !os.IsNotExist(err) {
				last = err
			}
		}
	}
	return last
}

var builtByRe = regexp.MustCompile(`^[0-9A-Za-z.+-]+$`)

// BaseLabel is the base vm was cloned from, for `list`: the agent-vm version
// that built it, and the day, "-" when not known. The base template
// describes itself. 0.1.0 recorded no version: a base without one was built
// by it.
func (d Dir) BaseLabel(vm, template string) string {
	verFile, byFile := VersionOf+vm, BuiltByOf+vm
	if vm == template {
		verFile, byFile = BaseVersion, BaseBuiltBy
	}
	by := d.Read(byFile)
	if !builtByRe.MatchString(by) {
		by = "0.1.0"
	}
	day := "-"
	if built := d.Read(verFile); built != "" {
		if sec, err := strconv.ParseInt(built, 10, 64); err == nil && sec >= 0 && strings.Trim(built, "0123456789") == "" {
			day = time.Unix(sec, 0).Format("2006-01-02")
		}
	}
	return by + " " + day
}
