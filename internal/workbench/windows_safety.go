package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/diagnostics"
	"github.com/sylvinus/agent-vm/internal/guestos"
)

func (s *Server) windowsGuestObservation(ctx context.Context, input map[string]any) (guestObservation, error) {
	var observation guestObservation
	cfg, err := loadConfig(s.configPath)
	if err != nil {
		return observation, err
	}
	input["timezone"] = cfg.Timezone
	cmd, err := guestos.WindowsRelayCommand(ctx, s.profile, "safety")
	if err != nil {
		return observation, err
	}
	data, err := json.Marshal(input)
	if err != nil {
		return observation, err
	}
	cmd.Stdin = bytes.NewReader(append(data, '\n'))
	cmd.Stderr = io.Discard
	var output bytes.Buffer
	cmd.Stdout = &boundedWindowsStatusWriter{buffer: &output, remaining: 64 << 10}
	if err = cmd.Run(); err != nil {
		return observation, errors.New("Windows 客机检测未返回完整结果")
	}
	var failure struct {
		Error      string `json:"error"`
		Stage      string `json:"stage"`
		Code       string `json:"error_code"`
		NativeCode int    `json:"native_code"`
	}
	if json.Unmarshal(output.Bytes(), &failure) == nil && failure.Error == "safety_probe_failed" {
		stages := map[string]bool{"request": true, "environment": true, "network": true, "public-tcp": true, "udp": true, "dns": true, "project-grant": true, "shared-read": true, "shared-write": true, "private-scope": true}
		codes := map[string]bool{"invalid_probe": true, "access_denied": true, "file_io": true, "socket": true, "probe_failed": true}
		if stages[failure.Stage] && codes[failure.Code] && failure.NativeCode >= 0 && failure.NativeCode <= 65535 {
			_ = diagnostics.New(s.profile).Record("warn", "windows-safety", fmt.Sprintf("stage=%s code=%s native_code=%d", failure.Stage, failure.Code, failure.NativeCode))
		}
	}
	return decodeWindowsObservation(output.Bytes())
}

func decodeWindowsObservation(data []byte) (guestObservation, error) {
	var observation guestObservation
	var status struct {
		Error string `json:"error"`
		Stage string `json:"stage"`
	}
	if json.Unmarshal(data, &status) != nil {
		return observation, errors.New("Windows 客机检查没有完成，外网保持关闭；请查看具体检查阶段后重试")
	}
	if status.Error != "" {
		stage := map[string]string{"request": "请求校验", "environment": "系统读回", "network": "代理与 DNS", "public-tcp": "公网连接约束", "udp": "UDP 约束", "dns": "公共 DNS 约束", "project-grant": "项目授权", "shared-read": "共享文件读取", "shared-write": "共享文件写回", "private-scope": "私有目录约束"}[status.Stage]
		if stage == "" {
			stage = "客机"
		}
		return observation, errors.New("Windows " + stage + "检查没有完成，外网保持关闭")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if decoder.Decode(&observation) != nil || decoder.Decode(new(any)) != io.EOF {
		return observation, errors.New("Windows 客机检测返回无效结果")
	}
	if observation.TZ == "" || observation.SystemTimezone == "" || observation.ProcessLocale == "" || observation.SystemLocale == "" || !observation.LocaleValid {
		return observation, errors.New("Windows 客机检查缺少系统读回结果，外网保持关闭")
	}
	return observation, nil
}

func (s *Server) windowsVMLayout() (bool, bool) {
	ctx, cancel := s.controlContext()
	defer cancel()
	report, err := guestos.InspectHyperV(ctx, s.windowsGuest.VMID)
	if err != nil || report.VM == nil || report.Lookup != "found" {
		return false, false
	}
	vm := report.VM
	network := vm.NetworkAdapterCount == 0 && vm.Generation == 2 && vm.SecureBoot && vm.VirtualTPM
	projects, err := s.projectPaths()
	if err != nil {
		return false, network
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	allowed := make(map[string]bool, len(projects))
	for _, path := range projects {
		allowed[path] = true
	}
	for path, share := range s.projectShares {
		if share == nil || share.windowsProject == nil || !allowed[path] {
			return false, network
		}
		select {
		case <-share.done:
			return false, network
		default:
		}
	}
	share := s.projectShares[s.environmentDir()]
	return share != nil && share.windowsProject != nil && vm.DiskCount == 1 && vm.SingleLocalVHDX != nil && *vm.SingleLocalVHDX, network
}

func windowsGuestSettingsMatch(observation guestObservation, cfg cage.Config) bool {
	locale := strings.ReplaceAll(strings.TrimSuffix(cfg.Locale, ".UTF-8"), "_", "-")
	return observation.LocaleValid && effectiveWindowsZone(observation.SystemTimezone) == effectiveWindowsZone(cfg.Timezone) && effectiveWindowsZone(observation.TZ) == effectiveWindowsZone(cfg.Timezone) && strings.EqualFold(observation.ProcessLocale, locale) && strings.EqualFold(observation.SystemLocale, locale)
}

func effectiveWindowsZone(zone string) string {
	switch zone {
	case "Etc/UTC", "Etc/GMT", "UTC", "GMT":
		return "UTC"
	default:
		return zone
	}
}

func (s *Server) windowsBoundaryInput(root, private, shared, challenge string, input map[string]any) error {
	project, err := s.windowsProjectForHost(root)
	if err != nil || project == nil {
		return errors.New("Windows 环境目录尚未连接")
	}
	input["project_id"] = project.ID
	input["grant"] = project.Grant
	input["shared_name"] = filepath.Base(shared)
	input["private_name"] = filepath.Base(private)
	input["challenge"] = challenge
	delete(input, "private_file")
	delete(input, "shared_file")
	return nil
}

func (s *Server) applyWindowsGuestSettings(ctx context.Context, cfg cage.Config) error {
	cmd, err := guestos.WindowsRelayCommand(ctx, s.profile, "network-settings")
	if err != nil {
		return err
	}
	data, err := json.Marshal(windowsGuestSettingsInput(cfg))
	if err != nil {
		return err
	}
	cmd.Stdin = bytes.NewReader(append(data, '\n'))
	cmd.Stderr = io.Discard
	var output bytes.Buffer
	cmd.Stdout = &boundedWindowsStatusWriter{buffer: &output, remaining: 4096}
	if err = cmd.Run(); err != nil {
		return errors.New("Windows 客机系统设置未能完成确认，外网保持关闭；请重试应用并核对实际设置")
	}
	var result struct {
		OK bool `json:"ok"`
	}
	if json.Unmarshal(output.Bytes(), &result) != nil || !result.OK {
		return errors.New("Windows 客机系统设置未得到完成确认")
	}
	return nil
}

func windowsGuestSettingsInput(cfg cage.Config) map[string]string {
	return map[string]string{"action": "apply", "timezone": cfg.Timezone, "locale": cfg.Locale, "browser_language": cfg.BrowserLanguage}
}
