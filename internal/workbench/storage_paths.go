package workbench

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/guestos"
	"github.com/sylvinus/agent-vm/internal/mounts"
	"github.com/sylvinus/agent-vm/internal/paths"
)

func (s *Server) validateStoragePlacement(directory string) error {
	target, err := cage.StoragePhysicalPath(directory)
	if err != nil {
		return err
	}
	projects, err := s.projectPaths()
	if err != nil {
		return err
	}
	for _, project := range projects {
		physical, err := cage.StoragePhysicalPath(project)
		if err != nil {
			return err
		}
		if cage.StoragePathsOverlap(physical, target) {
			return errors.New("环境存储目录与已共享的项目重叠，请选择项目以外的专用文件夹")
		}
	}
	return nil
}

// Resolve both sides of the sharing boundary. A profile or retained backup
// can be reached through a Windows junction even if its saved path looks
// unrelated to the requested project.
func privateProjectDirectories(profile string) ([]string, error) {
	directories, err := cage.StoragePrivatePaths(profile)
	if err != nil {
		return nil, err
	}
	directories = append(directories, profile)
	directories = append(directories, guestos.WindowsPrivateStoragePaths()...)
	for _, directory := range directories {
		physical, err := cage.StoragePhysicalPath(directory)
		if os.IsNotExist(err) {
			// A detached backup drive still reserves its recorded location.
			continue
		}
		if err != nil {
			return nil, err
		}
		if !paths.Equal(physical, directory) {
			directories = append(directories, physical)
		}
	}
	return directories, nil
}

func validateHostProjectData(root, profile string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	defaultProfile := filepath.Join(home, ".claude-cage")
	// Independent profiles also protect disks and backups of the default workload.
	if !paths.Equal(profile, defaultProfile) {
		private, err := privateProjectDirectories(defaultProfile)
		if err != nil {
			return err
		}
		for _, dir := range private {
			if cage.StoragePathsOverlap(root, dir) {
				return errors.New("Cage 配置、环境存储及备份不能共享")
			}
		}
	}
	refs := mounts.Refs(paths.Host(home), "", paths.Host(defaultProfile))
	if _, unsafe := mounts.UnsafeProject(paths.Host(root), refs); unsafe {
		return errors.New("此目录包含或属于凭据、用户资料或版本控制数据，无法共享")
	}
	return nil
}
