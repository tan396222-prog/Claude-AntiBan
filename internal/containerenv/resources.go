package containerenv

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

const PIDLimit = 1024

type resourceEvidence struct {
	MemoryBytes int64 `json:"memory_bytes"`
	CPUs        int   `json:"cpus"`
	PIDs        int64 `json:"pids"`
}

func (r *Runtime) resourceSlice() string { return "cage" + r.identity + ".slice" }

// Read the host-owned parent cgroup, outside the guest's writable namespace,
// and verify that systemd placed this exact container scope underneath it.
func (r *Runtime) verifyResources(ctx context.Context, containerID string) (resourceEvidence, error) {
	return r.verifyHostResources(ctx, containerID)
}

func parseResourceEvidence(data string, configuration Config) (resourceEvidence, error) {
	lines := strings.SplitN(strings.TrimSpace(data), "\n", 4)
	if len(lines) != 4 {
		return resourceEvidence{}, errors.New("容器未返回完整 cgroup 资源限额")
	}
	memory, memoryErr := strconv.ParseInt(strings.TrimSpace(lines[0]), 10, 64)
	cpu := strings.Fields(lines[1])
	pids, pidErr := strconv.ParseInt(strings.TrimSpace(lines[2]), 10, 64)
	if memoryErr != nil || pidErr != nil || len(cpu) != 2 {
		return resourceEvidence{}, errors.New("容器 CPU、内存或进程数限额未生效")
	}
	quota, quotaErr := strconv.ParseInt(cpu[0], 10, 64)
	period, periodErr := strconv.ParseInt(cpu[1], 10, 64)
	if quotaErr != nil || periodErr != nil || period < 1000 || period > 1000000 || quota != int64(configuration.CPUs)*period || memory != configuration.MemoryBytes || pids != PIDLimit {
		return resourceEvidence{}, errors.New("容器内核资源限额与配置不一致，拒绝继续启动")
	}
	cgroup2 := false
	for _, mount := range strings.Split(lines[3], "\n") {
		fields := strings.Fields(mount)
		if len(fields) > 6 && fields[4] == "/sys/fs/cgroup" && strings.Contains(mount, " - cgroup2 ") {
			cgroup2 = true
		}
	}
	if !cgroup2 {
		return resourceEvidence{}, errors.New("容器资源限额缺少 cgroup v2 内核挂载证据")
	}
	return resourceEvidence{MemoryBytes: memory, CPUs: configuration.CPUs, PIDs: pids}, nil
}
