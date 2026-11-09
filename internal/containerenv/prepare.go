package containerenv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sylvinus/agent-vm/internal/host"
)

// Docker supplies a read-only cgroup mount. In the container's private cgroup
// and user namespaces, mount cgroup2 afresh so systemd sees only its delegated
// subtree. No host filesystem is bound into the container.
const dockerInit = `set -eu
umount /sys/fs/cgroup
mount -t cgroup2 none /sys/fs/cgroup
exec /sbin/init
`

func (r *Runtime) createArguments() []string {
	args := []string{"create", "--name", r.Name(), "--label", profileLabel + "=" + r.identity, "--network", "none", "--cgroupns", "private", "--cgroup-parent", r.resourceSlice(), "--ipc", "private", "--hostname", "cage", "--add-host", "cage:127.0.1.1", "--cpus", strconv.Itoa(r.config.CPUs), "--memory", strconv.FormatInt(r.config.MemoryBytes, 10), "--pids-limit", strconv.Itoa(PIDLimit), "--device", "/dev/fuse", "--device", "/dev/net/tun", "--cap-add", "SYS_ADMIN", "--cap-add", "NET_ADMIN", "--tmpfs", "/run", "--tmpfs", "/tmp", "--stop-signal", "SIGRTMIN+3", "--stop-timeout", "30"}
	if r.config.Engine == "podman" {
		return append(args, "--http-proxy=false", "--systemd", "always", "--pull", "never", "--entrypoint", "/sbin/init", r.config.Image)
	}
	return append(args, "--pull", "never", "--entrypoint", "/bin/sh", r.config.Image, "-c", dockerInit)
}

// Prepare creates one stopped, persistent container from a locally available
// pinned image. It never pulls an image, removes a container, or rebuilds an
// existing writable layer. Guest tooling is installed over the guarded SSH
// maintenance path after Start establishes the network and protected share.
func (r *Runtime) Prepare(ctx context.Context, log io.Writer) error {
	if err := host.EnsurePrivateDir(r.InstanceDir()); err != nil {
		return err
	}
	unlock, err := lockRuntime(ctx, filepath.Join(r.InstanceDir(), "lifecycle.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	rootless, err := r.rootlessEngine(ctx)
	if err != nil {
		return err
	}
	if !rootless {
		return errors.New("Cage 只允许使用独立 rootless 容器引擎")
	}
	if err := r.ensureResourceSlice(ctx); err != nil {
		return err
	}
	container, err := r.inspectContainer(ctx)
	if err != nil {
		return err
	}
	if container != nil {
		if err := checkContainerConfiguration(container); err != nil {
			return err
		}
		if container.HostConfig.CgroupParent != r.resourceSlice() {
			return errors.New("已有容器缺少宿主资源边界；未重建或删除原数据")
		}
		return r.recordStorage(ctx, container.ID)
	}
	for _, device := range []string{"/dev/fuse", "/dev/net/tun"} {
		info, err := os.Stat(device)
		if err != nil || info.Mode()&os.ModeDevice == 0 {
			return fmt.Errorf("服务器缺少容器所需设备 %s", device)
		}
	}
	image, err := r.output(ctx, "image", "inspect", "--format", "{{.Id}}", r.config.Image)
	if err != nil {
		return errors.New("指定的固定容器基础镜像尚未安装，请先完成服务器部署")
	}
	if !imageIDPattern.MatchString(strings.TrimSpace(string(image))) {
		return errors.New("容器基础镜像没有有效 SHA-256 标识")
	}
	if _, err := r.output(ctx, r.createArguments()...); err != nil {
		return err
	}
	container, err = r.inspectContainer(ctx)
	if err != nil {
		return err
	}
	if container == nil {
		return errors.New("容器创建后未能验证身份")
	}
	if err := checkContainerConfiguration(container); err != nil {
		return err
	}
	if err := r.recordStorage(ctx, container.ID); err != nil {
		return err
	}
	if log != nil {
		fmt.Fprintln(log, "Cage container prepared with a persistent filesystem and no default network.")
	}
	return nil
}

func (r *Runtime) recordStorage(ctx context.Context, containerID string) error {
	path, err := r.StoragePath(ctx)
	if err != nil {
		return err
	}
	return writePrivateJSON(filepath.Join(r.InstanceDir(), "storage.json"), map[string]string{"engine": r.config.Engine, "container_id": containerID, "storage_path": path})
}
