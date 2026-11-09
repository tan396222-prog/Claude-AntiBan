package gitguard

// Tests of the scan that do not compare with 0.2's: what it must find, and
// what it must say when it could not look.

import (
	"context"
	"os"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sylvinus/agent-vm/internal/mounts"
)

// scanBox is Scan of the box's project, with git reading the box's config.
func (b *box) scan(volumes ...string) *Result {
	b.t.Helper()
	b.useEnv()
	return Scan(context.Background(), Shares(b.proj, volumes), b.home)
}

func (b *box) newScan(volumes ...string) *scan {
	b.useEnv()
	shares := Shares(b.proj, volumes)
	return &scan{ctx: context.Background(), shares: shares, proj: shares[0], home: b.home, seen: map[string]bool{}}
}

func risksText(r *Result) string { return strings.Join(r.Risks(mounts.BaseNames), "\n") }

// A path with a newline or a tab in it is checked all the same: it was
// skipped, with nothing said.
func TestScanOddPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no newline in Windows paths")
	}
	for _, name := range []string{"pro\nj", "pro\tj", "pro\n"} {
		b := newBox(t)
		b.proj = b.home + "/" + name
		os.MkdirAll(b.proj, 0o755)
		b.git(b.proj, "init", "-q")
		b.git(b.proj, "config", "core.hooksPath", ".githooks")
		b.git(b.proj, "config", "core.fsmonitor", "tools/mon.sh")
		r := b.scan()
		if len(r.Hooks) != 1 || r.Hooks[0] != ".githooks\t.githooks" {
			t.Errorf("%q: hooks %q", name, r.Hooks)
		}
		if got := risksText(r); got != "core.fsmonitor = tools/mon.sh" {
			t.Errorf("%q: risks %q", name, got)
		}
	}
	// A hooks folder whose own name has them: listed with no name to give.
	b := newBox(t)
	b.git(b.proj, "config", "core.hooksPath", "hk\tx/h")
	b.git(b.proj, "config", "--add", "core.hooksPath", "hk\nx/h")
	r := b.scan()
	for _, h := range r.Hooks {
		if strings.ContainsAny(h[:strings.LastIndexByte(h, '\t')], "\t\n") || !strings.HasSuffix(h, "\t") {
			t.Errorf("hooks line %q", h)
		}
	}
	if len(r.Hooks) == 0 {
		t.Error("a hooks folder with a tab in its name was not listed")
	}
}

// What git could not tell is a risk, not an empty answer.
func TestScanGitFailures(t *testing.T) {
	// A config file git cannot read.
	b := newBox(t)
	cfg := b.proj + "/.git/config"
	old, _ := os.ReadFile(cfg)
	os.WriteFile(cfg, append(old, "[core\n"...), 0o644)
	got := risksText(b.scan())
	if !strings.Contains(got, "could not check the repository of the project: ") || !strings.Contains(got, "bad config") {
		t.Errorf("bad repository config:\n%s", got)
	}

	// git's own config.
	b = newBox(t)
	b.write(b.home+"/.gitconfig", "[core\n")
	got = risksText(b.scan())
	if !strings.Contains(got, "could not check git's own config: ") {
		t.Errorf("bad global config:\n%s", got)
	}
}

// A git that does not answer is a risk, and does not stall the scan.
func TestScanGitTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script for git")
	}
	b := newBox(t)
	fake := t.TempDir()
	os.WriteFile(fake+"/git", []byte("#!/bin/sh\nexec sleep 60\n"), 0o755)
	t.Setenv("PATH", fake+string(os.PathListSeparator)+os.Getenv("PATH"))
	old := gitTimeout
	gitTimeout = 300 * time.Millisecond
	t.Cleanup(func() { gitTimeout = old })
	start := time.Now()
	got := risksText(b.scan())
	if d := time.Since(start); d > 20*time.Second {
		t.Errorf("took %v", d)
	}
	if !strings.Contains(got, "could not check the repository of the project: git did not answer in time") {
		t.Errorf("risks:\n%s", got)
	}
	if st := BareState(context.Background()); st != BareUnavailable {
		t.Errorf("BareState with a hung git: %s", st)
	}
}

// An option's value, or an assignment, can be the command: ssh -o
// ProxyCommand=./proxy.sh, --upload-pack=./x.
func TestScanOptionValues(t *testing.T) {
	b := newBox(t)
	b.git(b.proj, "config", "core.sshCommand", "ssh -o ProxyCommand=tools/proxy.sh")
	b.git(b.proj, "config", "remote.o.uploadpack", "git-upload-pack --exec=./up.sh")
	b.git(b.proj, "config", "core.pager", `less -o "ProxyCommand=./pager.sh arg"`)
	b.git(b.proj, "config", "core.askPass", "env X=tools/ask.sh askpass")
	// No path in them, a path outside the shares, or a URL.
	b.git(b.proj, "config", "credential.helper", "store --file=y --path=/usr/bin/true")
	b.git(b.proj, "config", "alias.p", "!git push --repo=https://example.com/x")
	b.git(b.proj, "config", "core.editor", "vi -o StrictHostKeyChecking=no")
	got := risksText(b.scan())
	want := []string{
		"core.sshcommand = ssh -o ProxyCommand=tools/proxy.sh",
		"remote.o.uploadpack = git-upload-pack --exec=./up.sh",
		`core.pager = less -o "ProxyCommand=./pager.sh arg"`,
		"core.askpass = env X=tools/ask.sh askpass",
	}
	lines := strings.Split(got, "\n")
	slices.Sort(lines)
	slices.Sort(want)
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("risks:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}
}

// A hook that is another name for a file is that file: the VM, writing it
// where it is linked from a share, changes what git runs.
func TestScanHookHardLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("not checked on Windows")
	}
	b := newBox(t)
	b.write(b.proj+"/scripts/pre-commit", "#!/bin/sh\n")
	b.write(b.proj+"/.git/hooks/post-commit", "#!/bin/sh\n")
	b.write(b.proj+"/scripts/sample", "#!/bin/sh\n")
	b.write(b.proj+"/scripts/hidden", "#!/bin/sh\n")
	for hook, file := range map[string]string{"pre-commit": "pre-commit", "pre-push.sample": "sample", ".dot": "hidden"} {
		if err := os.Link(b.proj+"/scripts/"+file, b.proj+"/.git/hooks/"+hook); err != nil {
			t.Skip("no hard links here:", err)
		}
	}
	got := risksText(b.scan())
	if got != "hook pre-commit has other hard links: one may be in a share" {
		t.Errorf("risks:\n%s", got)
	}
}

// Where core.hooksPath can point, and what the scan makes of each.
func TestScanHooksPathVariants(t *testing.T) {
	nfd := "e\xcc\x81cole/hooks" // e + combining acute
	for _, c := range []struct {
		value string
		want  string // the hooks line, "" for none
	}{
		{".githooks", ".githooks\t.githooks"},
		{"./.githooks/", ".githooks\t.githooks"},
		{"tools/../.githooks", ".githooks\t.githooks"},
		{"~/proj/.githooks", ".githooks\t.githooks"},
		{"PROJ/.githooks", ".githooks\t.githooks"},
		{".GITHOOKS", ".GITHOOKS\t.GITHOOKS"},
		{".", ".\t."},
		{"../outside", ""},
		{"~/other/hooks", ""},
		{nfd, nfd + "\t" + nfd},
		{"ev\xe2\x80\xaeil/hooks", "ev?il/hooks\t"},           // U+202E
		{"zero\xe2\x80\x8bwidth/hooks", "zero?width/hooks\t"}, // U+200B
		{"my hooks/x", "my hooks/x\tmy hooks/x"},
		{".git/hooks", ""},
		{".GIT/hooks", ""},
	} {
		b := newBox(t)
		v := strings.ReplaceAll(c.value, "PROJ", b.proj)
		if v == nfd {
			b.setRaw("core", "hooksPath", v)
		} else {
			b.git(b.proj, "config", "core.hooksPath", v)
		}
		r := b.scan()
		got := strings.Join(r.Hooks, "\n")
		if got != c.want {
			t.Errorf("core.hooksPath = %q: hooks %q, want %q", c.value, got, c.want)
		}
	}
	// Names: the folder's first component as spelled, so that the server,
	// which compares names as the file system does, keeps every spelling.
	b := newBox(t)
	b.setRaw("core", "hooksPath", nfd)
	if names := b.scan().Names(); !slices.Contains(names, "e\xcc\x81cole") {
		t.Errorf("names: %q", names)
	}
	b = newBox(t)
	b.git(b.proj, "config", "core.hooksPath", "ev\xe2\x80\xaeil/hooks")
	for _, n := range b.scan().Names() {
		if strings.ContainsRune(n, 0x202e) {
			t.Errorf("a bidi override in the names: %q", n)
		}
	}
}

// A name accepted keeps a path read-only whatever its case; a spelling the
// comparison does not take for the name stays a risk to accept.
func TestRisksNameSpellings(t *testing.T) {
	b := newBox(t)
	b.git(b.proj, "config", "core.fsmonitor", "Tools/run.sh")
	b.setRaw("core", "pager", "e\xcc\x81cole/run.sh")
	r := b.scan()
	got := strings.Join(r.Risks([]string{".git", ".hg", "tools", "\xc3\xa9cole"}), "\n")
	if strings.Contains(got, "fsmonitor") {
		t.Errorf("Tools/ is not under the name tools:\n%s", got)
	}
	if !strings.Contains(got, "core.pager") {
		t.Errorf("the decomposed spelling is taken for the composed name:\n%s", got)
	}
	if got := strings.Join(r.Risks([]string{".git", ".hg", "tools", "e\xcc\x81cole"}), "\n"); strings.Contains(got, "core.pager") {
		t.Errorf("the name as spelled does not cover it:\n%s", got)
	}
}

// A bare repository in a share is a repository: its config's commands and its
// hooks count.
func TestScanBareRepo(t *testing.T) {
	b := newBox(t)
	for _, d := range []string{"mirror.git", "deep/er/bare.git"} {
		p := b.proj + "/" + d
		os.MkdirAll(p, 0o755)
		b.git(p, "init", "-q", "--bare")
		b.git(p, "config", "core.pager", "tools/p.sh")
		b.git(p, "config", "core.hooksPath", "hk")
	}
	r := b.scan()
	for _, want := range []string{"mirror.git/hk\tmirror.git/hk", "deep/er/bare.git/hk\tdeep/er/bare.git/hk"} {
		if !slices.Contains(r.Hooks, want) {
			t.Errorf("hooks %q lack %q", r.Hooks, want)
		}
	}
	// A bare repository's config is a file of the share, with no name to keep
	// it read-only; one risk per distinct text: both repositories say the
	// same of core.pager.
	got := risksText(r)
	for _, want := range []string{"config file mirror.git/config", "config file deep/er/bare.git/config"} {
		if !strings.Contains(got, want) {
			t.Errorf("risks lack %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "core.pager = tools/p.sh") != 1 {
		t.Errorf("risks:\n%s", got)
	}
}

// Where repositories are looked for: down to maxRepoDepth folders, not in a
// node_modules below the first level (find's -mindepth 2), nor in a .git; a
// bound on the folders read.
func TestReposWalk(t *testing.T) {
	b := newBox(t)
	for _, d := range []string{
		"node_modules/pkg", "a/node_modules/x", "a/b", "a/b/c/d", "l1/l2/l3/l4/l5/l6", "m1/m2/m3/m4/m5/m6/m7",
	} {
		os.MkdirAll(b.proj+"/"+d, 0o755)
		b.git(b.proj+"/"+d, "init", "-q")
	}
	s := b.newScan()
	var got []string
	for _, r := range s.repos(s.proj) {
		got = append(got, strings.TrimPrefix(strings.TrimPrefix(r.at, s.proj), "/"))
	}
	sort.Strings(got)
	want := []string{"", "a/b", "a/b/c/d", "l1/l2/l3/l4/l5/l6", "node_modules/pkg"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("repositories %q, want %q", got, want)
	}
	if len(s.risks) != 0 {
		t.Errorf("risks: %+v", s.risks)
	}

	old := maxFolders
	maxFolders = 3
	t.Cleanup(func() { maxFolders = old })
	s = b.newScan()
	s.repos(s.proj)
	if got := risksText(&Result{risks: s.risks}); !strings.Contains(got, "repositories in the project: stopped looking after 3 folders") {
		t.Errorf("risks:\n%s", got)
	}
}

func TestVisible(t *testing.T) {
	for in, want := range map[string]string{
		"core.editor = vi":  "core.editor = vi",
		"été 日本 😀":          "été 日本 😀",
		"a\x1b[2Jb":         "a?[2Jb",
		"a‮b":               "a?b",
		"a​b⁦c":             "a?b?c",
		"nb sp":             "nb?sp",
		"a b":               "a?b",
		"a\U000e0041b":      "a?b",
		"a\U0000feffb":      "a?b",
		"tab\there\nnl":     "tab?here?nl",
		"\u009b31m":         "?31m",
		"private\U000f0000": "private?",
	} {
		if got := visible(in); got != want {
			t.Errorf("visible(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBareStateScopes(t *testing.T) {
	b := newBox(t)
	b.useEnv()
	if st := BareState(context.Background()); st != BareUnset && st != BareOld {
		t.Errorf("unset: %s", st)
	}
	b.write(b.home+"/.gitconfig", "[safe]\n\tbareRepository = explicit\n")
	if st := BareState(context.Background()); st != BareOK && st != BareOld {
		t.Errorf("explicit in the global config: %s", st)
	}
	b.write(b.home+"/.gitconfig", "[safe]\n\tbareRepository = all\n")
	if st := BareState(context.Background()); st != BareUnset && st != BareOld {
		t.Errorf("all: %s", st)
	}
}
