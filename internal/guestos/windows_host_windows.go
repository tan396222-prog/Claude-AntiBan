//go:build windows

package guestos

import (
	"context"
	"debug/pe"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// probeWindowsHost checks the local platform without requiring VM-wide privileges.
// It doesn't create a VM or test whether a guest image can be installed.
func probeWindowsHost(ctx context.Context) (bool, string) {
	version := windows.RtlGetVersion()
	const workstation = 1 // VER_NT_WORKSTATION
	if version.ProductType != workstation || version.MajorVersion < 10 || version.BuildNumber < 22000 {
		return false, "Windows 原生客机需要 Windows 11 桌面版宿主。"
	}
	var processMachine, nativeMachine uint16
	if err := windows.IsWow64Process2(windows.CurrentProcess(), &processMachine, &nativeMachine); err != nil {
		return false, "无法读取 Windows 主机的原生处理器架构。"
	}
	if runtime.GOARCH != "amd64" || nativeMachine != pe.IMAGE_FILE_MACHINE_AMD64 {
		return false, "Windows 原生客机需要 Intel 或 AMD x64 主机及 x64 版 Cage。"
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	cmd, err := systemPowerShellQuery(ctx, hyperVHostProbe)
	if err != nil {
		return false, "无法定位系统自带的 Windows PowerShell。"
	}
	output, err := cmd.Output()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return false, "查询 Hyper-V 超时，请检查本机虚拟机管理服务后重试。"
	}
	if ctx.Err() != nil {
		return false, "已取消 Windows 主机预检。"
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 2 {
			if reason := strings.TrimSpace(string(output)); reason != "" {
				return false, reason
			}
		}
		return false, "无法通过系统 PowerShell 查询 Hyper-V，请检查 PowerShell 和 Windows 管理服务。"
	}
	if strings.TrimSpace(string(output)) != "ready" {
		return false, "Hyper-V 预检返回了无法识别的结果。"
	}
	return true, "Hyper-V 平台已就绪；首次安装确认系统授权后，Cage 管理助手仅操作已绑定客机。"
}

func systemPowerShellQuery(ctx context.Context, script string) (*exec.Cmd, error) {
	systemDir, err := windows.GetSystemDirectory()
	if err != nil {
		return nil, err
	}
	shell := filepath.Join(systemDir, "WindowsPowerShell", "v1.0", "powershell.exe")
	cmd := exec.CommandContext(ctx, shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd, nil
}

const hyperVHostProbe = `
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
function Unavailable([string]$reason) { Write-Output $reason; exit 2 }

$edition = (Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion' -Name EditionID).EditionID
if ($edition -like 'Core*') {
    Unavailable 'Windows 家庭版未提供完整的 Hyper-V 角色。此原型支持专业版、企业版和教育版。'
}
$service = Get-Service -Name vmms -ErrorAction SilentlyContinue
if (!$service) {
    Unavailable '未检测到 Hyper-V 虚拟机管理服务（vmms），请检查 Windows 功能中的 Hyper-V 平台。'
}
if ($service.Status -ne 'Running') {
    Unavailable 'Hyper-V 虚拟机管理服务（vmms）当前未运行。'
}
if ((Get-CimInstance Win32_ComputerSystem).HypervisorPresent -ne $true) {
    Unavailable 'Windows 未检测到正在运行的虚拟机监控程序，请检查固件虚拟化设置和 Hyper-V 启动状态。'
}
if (!(Get-Command 'Hyper-V\Get-VMHost' -ErrorAction SilentlyContinue)) {
    Unavailable '缺少 Hyper-V PowerShell 管理模块，请检查 Windows 功能中的 Hyper-V 管理工具。'
}
Write-Output 'ready'
`
