package limaembed

import (
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Share is Lima's share/lima, written out of the binary.
type Share struct {
	// Dir holds templates/ and the bundled guest agents.
	Dir         string
	guestAgents map[string]string
}

// Cage owns this pin independently of Lima's periodically regenerated templates.
//
//go:embed host-images/ubuntu-22.04.yaml
var ubuntu2204Image []byte

// ReadImageTemplate returns the pinned images bundled with this application.
func ReadImageTemplate(system string) ([]byte, error) {
	switch system {
	case "ubuntu-22.04":
		return ubuntu2204Image, nil
	case "debian-12", "debian-13", "ubuntu-24.04":
		return fs.ReadFile(assets, "assets/templates/_images/"+system+".yaml")
	default:
		return nil, fmt.Errorf("unsupported host image template %q", system)
	}
}

// Templates is where Lima reads templates from (LIMA_TEMPLATES_PATH).
func (s *Share) Templates() string {
	return filepath.Join(s.Dir, "templates")
}

// GuestAgent returns only helpers that were extracted from this executable.
// An older installation may have left other binaries in the same directory.
func (s *Share) GuestAgent(guestOS, arch string) (string, error) {
	name := "lima-guestagent." + guestOS + "-" + arch + ".gz"
	if agent, ok := s.guestAgents[name]; ok {
		return agent, nil
	}
	return "", fmt.Errorf("this build does not include the Lima guest agent for %s/%s", guestOS, arch)
}

// ErrNoAssets is returned by WriteShare when agent-vm was built without
// the guest agent (`go build` instead of `make`).
var ErrNoAssets = errors.New("this agent-vm was built without Lima's guest agent: build it with make")

// WriteShare writes the embedded files into dir/share/lima, each one that is
// missing or differs from the embedded copy, and returns where they are. The
// files are checked on every call: a damaged guest agent never reaches a VM.
func WriteShare(dir string) (*Share, error) {
	return writeShare(dir, assets)
}

func writeShare(dir string, files fs.FS) (*Share, error) {
	if _, err := fs.Stat(files, "assets/lima-guestagent.Linux-"+guestArch()+".gz"); err != nil {
		return nil, ErrNoAssets
	}
	share := &Share{Dir: filepath.Join(dir, "share", "lima"), guestAgents: make(map[string]string)}
	err := fs.WalkDir(files, "assets", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, "assets"), "/")
		if rel == "README" {
			return nil
		}
		dst := filepath.Join(share.Dir, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(dst, 0o700)
		}
		want, err := fs.ReadFile(files, p)
		if err != nil {
			return err
		}
		if err := writeIfChanged(dst, want); err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), "lima-guestagent.") && strings.HasSuffix(d.Name(), ".gz") {
			share.guestAgents[d.Name()] = dst
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("writing Lima's files to %s: %w", share.Dir, err)
	}
	return share, nil
}

func writeIfChanged(dst string, want []byte) error {
	if have, err := os.ReadFile(dst); err == nil && sha256.Sum256(have) == sha256.Sum256(want) {
		return nil
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, want, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// guestArch is this host's architecture as Lima names guest agents.
func guestArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	default:
		return runtime.GOARCH
	}
}
