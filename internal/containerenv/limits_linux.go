//go:build linux

package containerenv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	systemd "github.com/coreos/go-systemd/v22/dbus"
	"github.com/godbus/dbus/v5"
)

func userSystemd(ctx context.Context) (*systemd.Conn, error) {
	return systemd.NewConnection(func() (*dbus.Conn, error) {
		connection, err := dbus.Dial("unix:path=/run/user/"+strconv.Itoa(os.Getuid())+"/bus", dbus.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		if err = connection.Auth([]dbus.Auth{dbus.AuthExternal(strconv.Itoa(os.Getuid()))}); err == nil {
			err = connection.Hello()
		}
		if err != nil {
			connection.Close()
			return nil, err
		}
		return connection, nil
	})
}

// The outer slice belongs to the host user manager and remains outside the
// container's cgroup namespace. Guest root may manage its own systemd subtree
// but cannot raise these ancestor limits.
func (r *Runtime) ensureResourceSlice(ctx context.Context) error {
	connection, err := userSystemd(ctx)
	if err != nil {
		return fmt.Errorf("无法连接宿主用户 systemd 以设置容器资源上限: %w", err)
	}
	defer connection.Close()
	properties := []systemd.Property{
		systemd.PropDescription("Cage container " + r.identity),
		{Name: "MemoryMax", Value: dbus.MakeVariant(uint64(r.config.MemoryBytes))},
		{Name: "CPUQuotaPerSecUSec", Value: dbus.MakeVariant(uint64(r.config.CPUs) * 1000000)},
		{Name: "TasksMax", Value: dbus.MakeVariant(uint64(PIDLimit))},
	}
	// Querying a valid slice name first can make systemd synthesize an inactive
	// default unit. Creating the transient unit directly also handles that
	// implicit unit, without mistaking it for someone else's configured slice.
	result := make(chan string, 1)
	_, err = connection.StartTransientUnitContext(ctx, r.resourceSlice(), "fail", properties, result)
	if err != nil {
		var busError dbus.Error
		if !errors.As(err, &busError) || busError.Name != "org.freedesktop.systemd1.UnitExists" {
			return err
		}
		current, err := connection.GetUnitPropertiesContext(ctx, r.resourceSlice())
		if err != nil {
			return err
		}
		if current["Description"] != "Cage container "+r.identity {
			return errors.New("容器资源 slice 名称已被其他单元占用")
		}
		if err := connection.SetUnitPropertiesContext(ctx, r.resourceSlice(), true, properties[1:]...); err != nil {
			return err
		}
		if _, err := connection.StartUnitContext(ctx, r.resourceSlice(), "replace", result); err != nil {
			return err
		}
	}
	select {
	case state := <-result:
		if state != "done" {
			return fmt.Errorf("容器资源 slice 启动失败: %s", state)
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	_, err = r.readParentLimits(ctx, connection, "")
	return err
}

func (r *Runtime) readParentLimits(ctx context.Context, connection *systemd.Conn, containerID string) (resourceEvidence, error) {
	parent, err := connection.GetAllPropertiesContext(ctx, r.resourceSlice())
	if err != nil {
		return resourceEvidence{}, err
	}
	if parent["Description"] != "Cage container "+r.identity || parent["ActiveState"] != "active" {
		return resourceEvidence{}, errors.New("容器的宿主资源边界未运行")
	}
	parentPath, ok := parent["ControlGroup"].(string)
	if !ok || !strings.HasPrefix(parentPath, "/") || filepath.Clean(parentPath) != parentPath || filepath.Base(parentPath) != r.resourceSlice() {
		return resourceEvidence{}, errors.New("宿主资源 cgroup 路径无效")
	}
	if containerID != "" {
		prefix := "docker-"
		if r.config.Engine == "podman" {
			prefix = "libpod-"
		}
		scope, err := connection.GetAllPropertiesContext(ctx, prefix+containerID+".scope")
		if err != nil {
			return resourceEvidence{}, err
		}
		path, ok := scope["ControlGroup"].(string)
		if !ok || !strings.HasPrefix(path, parentPath+"/") || scope["ActiveState"] != "active" {
			return resourceEvidence{}, errors.New("实际容器未处于 Cage 的宿主资源边界内")
		}
	}
	root := filepath.Join("/sys/fs/cgroup", parentPath)
	var evidence strings.Builder
	for _, file := range []string{"memory.max", "cpu.max", "pids.max"} {
		data, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			return resourceEvidence{}, err
		}
		evidence.WriteString(strings.TrimSpace(string(data)))
		evidence.WriteByte('\n')
	}
	mounts, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return resourceEvidence{}, err
	}
	evidence.Write(mounts)
	return parseResourceEvidence(evidence.String(), r.config)
}

func (r *Runtime) verifyHostResources(ctx context.Context, containerID string) (resourceEvidence, error) {
	connection, err := userSystemd(ctx)
	if err != nil {
		return resourceEvidence{}, err
	}
	defer connection.Close()
	return r.readParentLimits(ctx, connection, containerID)
}
