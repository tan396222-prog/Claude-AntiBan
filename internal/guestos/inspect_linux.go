package guestos

import (
	"bytes"
	"context"
	"runtime"
	"strings"

	"github.com/sylvinus/agent-vm/internal/host"
)

func Inspect(_ context.Context) Inspection {
	r := Inspection{Guest: Debian13, WorkbenchSupport: "supported"}
	if runtime.GOARCH != "amd64" {
		r.WorkbenchSupport = "unsupported-host"
		r.Detail = "Linux 桌面发行包面向 x86_64 主机。"
		return r
	}
	var details bytes.Buffer
	r.HostCheckPassed = host.LinuxPrereqs(&details)
	r.Detail = strings.TrimSpace(details.String())
	if r.HostCheckPassed {
		r.Detail = "已检测到 Debian 工作台所需的 Linux 虚拟化组件。"
	}
	return r
}
