package workbench

import (
	"strconv"
	"strings"
)

type SetupProgress struct {
	Phase    string `json:"phase"`
	Label    string `json:"label"`
	Received *int64 `json:"received,omitempty"`
	Total    int64  `json:"total,omitempty"`
}

// Only fixed component names from the installer are displayed. Other output
// continues through the existing redacted operation log.
func parseSetupProgress(line string) *SetupProgress {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "CAGE_IMAGE_BYTES ") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil
		}
		received, err := strconv.ParseInt(fields[1], 10, 64)
		total, te := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || te != nil || received < 0 || total < -1 || (total > 0 && received > total) {
			return nil
		}
		return &SetupProgress{"image", "正在下载 Debian 13 镜像", &received, max(total, 0)}
	}
	if strings.HasPrefix(line, "CAGE_TOOL_BYTES ") {
		fields := strings.Fields(line)
		if len(fields) != 3 && len(fields) != 4 {
			return nil
		}
		received, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || received < 0 {
			return nil
		}
		p := &SetupProgress{Phase: "tools", Received: &received}
		if len(fields) == 4 {
			total, err := strconv.ParseInt(fields[3], 10, 64)
			if err != nil || total <= 0 || received > total {
				return nil
			}
			p.Total = total
		}
		switch fields[1] {
		case "mise.tar.gz":
			p.Phase, p.Label = "packages", "正在下载 mise"
		case "code-server.deb":
			p.Label = "正在下载网页编辑器 code-server"
		case "chromium.deb":
			p.Phase, p.Label = "packages", "正在下载 Chromium 浏览器"
		case "chromium-common.deb":
			p.Phase, p.Label = "packages", "正在下载 Chromium 公共资源"
		case "claude-code-linux-x64.tgz", "claude-code-linux-arm64.tgz":
			p.Label = "正在下载 Claude Code"
		default:
			return nil
		}
		return p
	}
	switch line {
	case "CAGE_IMAGE_READY":
		return &SetupProgress{Phase: "image", Label: "正在启动 Linux 基础环境"}
	case "CAGE_IMAGE_VERIFY":
		return &SetupProgress{Phase: "image", Label: "正在校验 Debian 镜像"}
	case "Installing packages inside VM...", "Installing base packages...":
		return &SetupProgress{Phase: "packages", Label: "正在安装 Linux 基础软件包"}
	case "Installing Python 3...":
		return &SetupProgress{Phase: "packages", Label: "正在安装 Python"}
	case "Installing Chromium...":
		return &SetupProgress{Phase: "packages", Label: "正在安装 Chromium 浏览器"}
	case "Installing Node.js 24...":
		return &SetupProgress{Phase: "packages", Label: "正在安装 Node.js 24"}
	case "Installing mise...":
		return &SetupProgress{Phase: "packages", Label: "正在安装 mise"}
	case "Installing Claude Code...":
		return &SetupProgress{Phase: "tools", Label: "正在安装 Claude Code"}
	case "Installing code-server...":
		return &SetupProgress{Phase: "tools", Label: "正在安装网页编辑器 code-server"}
	}
	if strings.HasPrefix(line, "Running custom setup from ") {
		return &SetupProgress{Phase: "services", Label: "正在配置桌面服务"}
	}
	return nil
}

func (e *jobExecution) setSetupProgress(p *SetupProgress) {
	e.owner.mu.Lock()
	defer e.owner.mu.Unlock()
	if e.id != "" && e.id == e.owner.state.ID && e.owner.state.Running {
		e.owner.state.SetupProgress = p
	}
}
