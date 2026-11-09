package workbench

import (
	"errors"
	"os"

	"github.com/sylvinus/agent-vm/internal/cage"
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
