package workbench

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/sylvinus/agent-vm/internal/host"
	"github.com/sylvinus/agent-vm/internal/remotehost"
)

func importDesktopConnections(full, entry string) error {
	old := []string{full, filepath.Join(full, "client")}
	if err := remotehost.NewStore(entry).ImportOnce(old...); err != nil {
		return err
	}
	marker := filepath.Join(entry, "downloads-imported.json")
	if err := host.CheckPrivateFile(marker); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	// Import private records only, never copy a downloaded file or follow a
	// saved user path. A retry after interruption preserves the newest record.
	for _, profile := range old {
		root := filepath.Join(profile, "remote-downloads")
		info, err := os.Lstat(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return errors.New("旧入口下载记录目录无效")
		}
		instances, err := os.ReadDir(root)
		if err != nil {
			return err
		}
		for _, instance := range instances {
			if !instance.IsDir() || !remoteInstanceID.MatchString(instance.Name()) {
				continue
			}
			from := filepath.Join(root, instance.Name())
			files, err := os.ReadDir(filepath.Join(from, "browser-downloads"))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			to := filepath.Join(entry, "remote-downloads", instance.Name())
			for _, file := range files {
				id := strings.TrimSuffix(file.Name(), ".json")
				if !file.Type().IsRegular() || !browserFileID.MatchString(id) {
					continue
				}
				record, err := readSavedDownload(from, id)
				if err != nil || record.InstanceID != instance.Name() {
					continue
				}
				current, err := readSavedDownload(to, id)
				if err == nil && !record.SavedAt.After(current.SavedAt) {
					continue
				}
				if err != nil && !os.IsNotExist(err) {
					return err
				}
				if err = writePrivate(savedDownloadPath(to, id), record); err != nil {
					return err
				}
			}
		}
	}
	return writePrivate(marker, struct{}{})
}
