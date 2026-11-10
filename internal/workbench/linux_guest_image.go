package workbench

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/sylvinus/agent-vm/internal/guestos"
	"github.com/sylvinus/agent-vm/internal/hostimage"
	"github.com/sylvinus/agent-vm/internal/limaembed"
)

func linuxGuestSystems(platform, arch string) []string {
	if platform == "darwin" && arch == "arm64" || (platform == "windows" || platform == "linux") && arch == "amd64" {
		return []string{guestos.Ubuntu2204}
	}
	return nil
}

func linuxGuestImagePlan(ctx context.Context, profile, system, arch string) (hostimage.Plan, error) {
	if system != guestos.Ubuntu2204 || arch != "amd64" && arch != "arm64" {
		return hostimage.Plan{}, errors.New("此主机暂不支持所选 Linux 客机镜像")
	}
	template, err := limaembed.ReadImageTemplate(system)
	if err != nil {
		return hostimage.Plan{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return hostimage.Plan{}, err
	}
	identity := hostimage.Identity{System: system, Version: "22.04", Arch: arch}
	// Resolution of a pinned Linux image is local. Downloads use the existing
	// installer transport only after the user submits the installation.
	return hostimage.Resolve(ctx, identity, filepath.Join(profile, "state", "guest-media", system), filepath.Join(home, "Downloads"), template, nil)
}

func (s *Server) installedLinuxSystem() (string, error) {
	return hostimage.LinuxSystem(filepath.Join(s.profile, "state"))
}

func canInstallCatalogLinux(system string) bool {
	for _, candidate := range linuxGuestSystems(runtime.GOOS, runtime.GOARCH) {
		if system == candidate {
			return true
		}
	}
	return false
}
