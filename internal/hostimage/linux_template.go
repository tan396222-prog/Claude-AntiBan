package hostimage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

func linuxTemplateData(plan Plan) ([]byte, error) {
	// These supported cloud images need no distribution-specific template
	// settings. Keep one pinned local image, with no network image fallback.
	value := map[string]any{
		"minimumLimaVersion": "2.0.0",
		"images":             []map[string]string{{"location": plan.Path, "arch": map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[plan.Host.Arch], "digest": plan.Digest}},
	}
	return json.Marshal(value)
}

func WriteLinuxTemplate(state string, plan Plan) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	if !IsLinux(plan.Host.System) || plan.Path == "" || plan.Digest == "" {
		return errors.New("Linux 系统材料尚未准备好")
	}
	data, err := linuxTemplateData(plan)
	if err != nil {
		return err
	}
	dir := filepath.Join(state, "guest-media")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(dir, "host-system.yaml")
	if current, err := os.ReadFile(path); err == nil {
		if string(current) != string(data) {
			return errors.New("Linux 安装已绑定另一份系统材料")
		}
		return Save(dir, plan)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.CreateTemp(dir, ".host-system-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	if err = errors.Join(err, file.Close()); err != nil {
		return err
	}
	if err = os.Rename(file.Name(), path); err != nil {
		return err
	}
	return Save(dir, plan)
}

// ClearLinuxTemplate releases an unstarted choice while retaining its media.
func ClearLinuxTemplate(state string, plan Plan) error {
	dir := filepath.Join(state, "guest-media")
	path := filepath.Join(dir, "host-system.yaml")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	expected, err := linuxTemplateData(plan)
	if err != nil || string(expected) != string(data) {
		return errors.New("本机系统模板归属已变化")
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, sourceRecord)); !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func LinuxTemplate(state string) (string, error) {
	path := filepath.Join(state, "guest-media", "host-system.yaml")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if _, recordErr := os.Lstat(filepath.Join(state, "guest-media", sourceRecord)); !errors.Is(recordErr, os.ErrNotExist) {
			return "", errors.New("所选 Linux 系统的安装模板缺失，请恢复原环境资料；不会改用其他系统")
		}
		return "template:debian-13", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 32<<10 {
		return "", errors.New("本机系统安装模板无效")
	}
	plan, err := Load(filepath.Join(state, "guest-media"))
	if err != nil || !IsLinux(plan.Host.System) {
		return "", errors.New("所选 Linux 系统来源记录无效，请恢复原环境资料")
	}
	expected, err := linuxTemplateData(plan)
	data, readErr := os.ReadFile(path)
	if err != nil || readErr != nil || string(data) != string(expected) {
		return "", errors.New("所选 Linux 系统的模板与来源不一致，请核对原环境资料")
	}
	return path, nil
}

// LinuxSystem reads the durable image choice, including after installation
// has finished and its temporary installation record has been removed.
func LinuxSystem(state string) (string, error) {
	path, err := LinuxTemplate(state)
	if err != nil {
		return "", err
	}
	if path == "template:debian-13" {
		return "debian-13", nil
	}
	plan, err := Load(filepath.Join(state, "guest-media"))
	return plan.Host.System, err
}
