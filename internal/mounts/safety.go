// Package mounts is what a project's VM is given of this machine: the
// project, and the entries of ~/.agent-vm/volumes, each checked first.
package mounts

import (
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sylvinus/agent-vm/internal/paths"
)

// BaseNames are always read-only in every share. .hg: Mercurial runs the
// hooks of .hg/hgrc in a repository the user owns, which files written
// through a share are. .sl (Sapling) and .jj (Jujutsu) keep a repository's
// config, which names commands to run, in the same way. .lfsconfig is not
// one: git-lfs reads only a few URL and filter keys from it, no command.
var BaseNames = []string{".git", ".hg", ".sl", ".jj"}

// UnderName reports whether the relative path rel goes through one of names
// (BaseNames when nil), matched as the SFTP server does: per component,
// whatever the case.
func UnderName(rel string, names []string) bool {
	if names == nil {
		names = BaseNames
	}
	for _, c := range strings.Split(rel, "/") {
		for _, n := range names {
			if c != "" && strings.EqualFold(c, n) {
				return true
			}
		}
	}
	return false
}

// Ref is a location no share may reach.
type Ref struct {
	What string // "your home directory", ...
	Path string // physical, folded
}

// SelfRef is the What of the Ref to agent-vm's own folder.
const SelfRef = "agent-vm itself"

const homeRef = "your home directory"

// Refs are the locations no share may reach: the home folder (dotfiles,
// SSH keys, every other project), agent-vm itself (the host runs its
// files), agent-vm's state (every VM's env) and Lima's homes, agent-vm's
// and the user's own (the VMs' SSH key and disks, its _config adding
// mounts to every VM).
func Refs(home, self, state string, limaHomes ...string) []Ref {
	refs := []Ref{{"your home directory", home}, {SelfRef, self}, {"agent-vm's state", state}}
	for _, h := range limaHomes {
		refs = append(refs, Ref{"Lima's state", h})
	}
	var out []Ref
	for _, r := range refs {
		if r.Path == "" {
			continue
		}
		// Not resolved when it does not exist: as typed, never emptied, or
		// every path would be inside it.
		if p, err := paths.Real(r.Path); err == nil && p != "" {
			r.Path = p
		} else {
			r.Path = strings.TrimSuffix(r.Path, "/")
		}
		if r.Path == "" {
			continue
		}
		r.Path = paths.Fold(r.Path)
		out = append(out, r)
	}
	return out
}

// UnsafeLocation says why dir must not be shared with a VM, writable or
// not, when it is or contains the home folder, or is, contains or is inside
// one of the other refs. A project inside the home folder is what is
// expected. Compared as physical paths, whatever the case where the file
// system ignores it.
func UnsafeLocation(dir string, refs []Ref) (string, bool) {
	if p, err := paths.Real(dir); err == nil {
		dir = p
	}
	d := strings.TrimSuffix(paths.Fold(dir), "/") + "/"
	for _, r := range refs {
		p := strings.TrimSuffix(r.Path, "/") + "/"
		if strings.HasPrefix(p, d) {
			return "is, or contains, " + r.What, true
		}
		if r.What != "your home directory" && strings.HasPrefix(d, p) {
			return "is inside " + r.What, true
		}
	}
	return "", false
}

// GitDirShare says why dir must not be a writable share: it is, or is
// inside, a repository's own folder: a .git or .hg (by name, as the SFTP
// server matches them), or a bare repository (HEAD, objects/ and refs/, as
// git finds one). The read-only names apply below a share's root, so such a
// share would be writable whole, hooks and config included.
func GitDirShare(dir string) (string, bool) {
	if p, err := paths.Real(dir); err == nil {
		dir = p
	}
	// dir and each folder above it, the root ("/", "C:/") excepted.
	root := paths.Root(dir)
	for a := strings.TrimSuffix(dir, "/"); a+"/" != root; {
		if isFile(a+"/HEAD") && isDir(a+"/objects") && isDir(a+"/refs") {
			return fmt.Sprintf("is, or is inside, a git repository's own folder (%s)", a), true
		}
		i := strings.LastIndexByte(a, '/')
		if i < 0 {
			break
		}
		a = a[:i]
	}
	if UnderName(strings.TrimPrefix(dir, root), nil) {
		return "is, or is inside, a folder git or Mercurial keeps its repository in (.git, .hg)", true
	}
	return "", false
}

// credentialRel are the places under the home folder where credentials and
// applications' data live: keys, cloud and cluster logins, password managers,
// browser profiles, keyrings. None is a project; one shared with a VM, read
// or write, is a secret given away. A project may be anywhere else in the
// home folder, so these are named. Lowercase-folded where case is ignored.
var credentialRel = []string{
	".ssh", ".aws", ".gnupg", ".pki", ".kube", ".docker", ".azure", ".oci", ".terraform.d",
	".config", ".password-store", ".1password", ".op", ".mozilla", ".thunderbird", ".var", "snap",
	".local/share/keyrings", ".local/share/gnome-keyring", ".local/share/kwalletd",
	"Library", "AppData",
}

// credentialAbs are the environment variables naming a folder of that kind
// outside the home folder's defaults.
var credentialAbs = []string{"APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME"}

// CredentialLocation says why dir must not be shared with a VM: it is,
// contains or is inside a folder of credentials or applications' data under
// the home folder (see credentialRel), found in refs. Those folders resolved
// through their links count too, as dir does.
func CredentialLocation(dir string, refs []Ref) (string, bool) {
	home := ""
	for _, r := range refs {
		if r.What == homeRef {
			home = r.Path
		}
	}
	if home == "" {
		return "", false
	}
	if p, err := paths.Real(dir); err == nil {
		dir = p
	}
	d := strings.TrimSuffix(paths.Fold(dir), "/") + "/"
	check := func(shown, p string) (string, bool) {
		for _, c := range []string{p, realOr(p)} {
			c = strings.TrimSuffix(paths.Fold(c), "/") + "/"
			if strings.HasPrefix(d, c) || strings.HasPrefix(c, d) {
				return "is, contains or is inside " + shown + ", which holds credentials or applications' data", true
			}
		}
		return "", false
	}
	for _, rel := range credentialRel {
		if why, ok := check("~/"+rel, strings.TrimSuffix(home, "/")+"/"+rel); ok {
			return why, true
		}
	}
	for _, v := range credentialAbs {
		if p := paths.Host(os.Getenv(v)); p != "" && paths.IsAbs(p) {
			if why, ok := check(p, p); ok {
				return why, true
			}
		}
	}
	return "", false
}

// realOr is p with its links resolved when it exists, else p.
func realOr(p string) string {
	if r, err := paths.Real(p); err == nil {
		return r
	}
	return p
}

// UnsafeProject says why the project dir must not be shared: see
// UnsafeLocation, CredentialLocation and GitDirShare.
func UnsafeProject(dir string, refs []Ref) (string, bool) {
	if why, ok := UnsafeLocation(dir, refs); ok {
		return why, true
	}
	if why, ok := CredentialLocation(dir, refs); ok {
		return why, true
	}
	return GitDirShare(dir)
}

// Unmountable says why Lima cannot be given dir as a share: with whitespace
// the mount fails silently, leaving a bare, root-owned mount point; a quote,
// a backslash or a control character are refused as 0.2 refused them; and
// what Lima would read as another path (Unwritable).
func Unmountable(dir string) (string, bool) {
	if strings.IndexFunc(dir, unicode.IsSpace) >= 0 {
		return "contains whitespace, which Lima cannot mount", true
	}
	if strings.ContainsAny(dir, "\"\\") || strings.IndexFunc(dir, unicode.IsControl) >= 0 {
		return "contains a quote, a backslash or a control character", true
	}
	return Unwritable(dir)
}

// Unwritable says why s cannot go into Lima's config as written: Lima
// expands Go templates in share paths ({{.Home}} is the home folder), and a
// byte that is not UTF-8 becomes U+FFFD on the way.
func Unwritable(s string) (string, bool) {
	if strings.Contains(s, "{{") {
		return "contains {{, which Lima reads as a template", true
	}
	if !utf8.ValidString(s) {
		return "is not valid UTF-8", true
	}
	return "", false
}

func isFile(p string) bool { fi, err := os.Stat(p); return err == nil && fi.Mode().IsRegular() }
func isDir(p string) bool  { fi, err := os.Stat(p); return err == nil && fi.IsDir() }
