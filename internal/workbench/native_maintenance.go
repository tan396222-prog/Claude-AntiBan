package workbench

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/guestos"
)

//go:embed native_component_probe.py
var nativeComponentProbe string

type nativeComponentSnapshot struct {
	Manifest  *cage.GuestManifest `json:"manifest"`
	Tools     map[string]string   `json:"tools"`
	Installed bool                `json:"installed"`
	Missing   []string            `json:"missing"`
	Error     string              `json:"error"`
}

func (s *Server) nativeComponentKit() (string, string) {
	system := guestos.Windows11
	if s.macosGuest != nil {
		system = guestos.MacOS
	}
	return filepath.Join(filepath.Dir(s.exe), "guest-os"), system
}

func (s *Server) readNativeComponents(ctx context.Context) (nativeComponentSnapshot, error) {
	var snapshot nativeComponentSnapshot
	if s.macosGuest != nil {
		command := shellescape.QuoteCommand([]string{"/usr/local/libexec/cage-cli/bin/python3", "-I", "-c", nativeComponentProbe})
		data, err := s.guest(ctx, "exec "+command)
		if err != nil {
			return snapshot, err
		}
		return snapshot, json.Unmarshal(data, &snapshot)
	}
	cmd, err := guestos.WindowsRelayCommand(ctx, s.profile, "versions")
	if err != nil {
		return snapshot, err
	}
	var data bytes.Buffer
	cmd.Stdout = &boundedWindowsStatusWriter{buffer: &data, remaining: 64 << 10}
	cmd.Stderr = io.Discard
	if err = cmd.Run(); err != nil {
		return snapshot, err
	}
	return snapshot, json.Unmarshal(data.Bytes(), &snapshot)
}

func (s *Server) nativeEnvironmentVersions(w http.ResponseWriter, r *http.Request) {
	kit, system := s.nativeComponentKit()
	required, kitError := guestos.RequiredNativeComponents(kit, system)
	components := componentVersions{Status: "stopped", Required: required.GuestManifest}
	tools := map[string]toolVersion{}
	for name, version := range required.Tools {
		tools[name] = toolVersion{Required: version}
	}
	detail := ""
	running := s.vmRunning()
	installed := false
	if running {
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		snapshot, err := s.readNativeComponents(ctx)
		cancel()
		components.Current = snapshot.Manifest
		components.UpdateRequired = true
		components.Status = "update_required"
		installed = snapshot.Installed
		if err != nil {
			detail = "客机组件版本暂时无法核验。旧环境可以同步环境组件；请先登录客机，再重试。"
		} else {
			detail = snapshot.Error
			if snapshot.Manifest != nil && kitError == nil && snapshot.Installed && snapshot.Manifest.Protocol == required.Protocol && snapshot.Manifest.Revision == required.Revision {
				components.Status = "current"
				components.UpdateRequired = false
			}
			for name, current := range snapshot.Tools {
				item := tools[name]
				item.Current = current
				tools[name] = item
			}
			if len(snapshot.Missing) > 0 {
				detail = "部分客机组件缺失或内容已变化，请同步环境组件。"
			}
		}
	}
	if kitError != nil {
		components.Status = "unknown"
		components.UpdateRequired = false
		detail = "此应用的原生组件包无法核验，请重新安装完整的 Cage 应用。"
	}
	s.mu.Lock()
	busy := s.jobs.snapshot().Running
	identity := s.identity
	s.mu.Unlock()
	jsonReply(w, map[string]any{"vm_running": running, "busy": busy, "components": components, "controller": identity, "tools": tools, "installed": installed,
		"tools_update_allowed": running && kitError == nil && !busy, "error": detail,
		"maintenance": map[string]any{"native": true, "guest_system": system, "requires_closed_terminals": true, "requires_system_authorization": true}})
}

func (s *Server) nativeMaintenancePreflight() error {
	if s.macosGuest == nil && s.windowsGuest == nil {
		return nil
	}
	if s.terminals.hasLive() || len(s.mobileCLI.activeAccounts()) != 0 {
		return errors.New("请先保存工作，结束正在运行的终端和 Claude Code 轮次，再重试更新。更新会重启客机连接组件；聊天历史、项目和登录资料会保留。")
	}
	if !s.vmRunning() {
		return errors.New("请先启动并登录客机，再更新环境组件。")
	}
	return nil
}

func (s *Server) maintainNativeComponents(tools bool) error {
	if err := s.nativeMaintenancePreflight(); err != nil {
		return err
	}
	kit, system := s.nativeComponentKit()
	manifest, err := guestos.RequiredNativeComponents(kit, system)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(s.profile, "native-components-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	data, err := json.Marshal(manifest)
	if err == nil {
		_, err = file.Write(data)
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	if err = s.closeProxyGate("正在更新原生客机组件，外网等待重新检查"); err != nil {
		return err
	}
	s.mu.Lock()
	s.ready = false
	s.guestUpdateRequired = true
	s.guestUpdateError = "原生组件更新尚未完成，可以保留当前环境重试。"
	s.mu.Unlock()
	s.closeWindowsBrowser()
	s.closeWindowsNetwork()
	s.closeProjectShares()
	s.setMessage("正在更新原生客机组件，保留系统磁盘、账户和项目数据…")
	var command *exec.Cmd
	if system == guestos.MacOS {
		command = exec.CommandContext(s.operationContext(), s.exe, "guest-os", "macos", "update-components", "--manifest", file.Name())
		if tools {
			command.Args = append(command.Args, "--tools")
		}
		command.Env = append(os.Environ(), cage.ConfigEnv+"="+s.configPath)
	} else {
		command, err = guestos.WindowsMaintenanceCommand(s.operationContext(), s.profile, kit, file.Name(), tools)
		if err != nil {
			return err
		}
	}
	command.Stdout, command.Stderr = s.jobs.execution(), s.jobs.execution()
	if err = command.Run(); err != nil {
		return executionFailure("更新原生客机组件", err, s.jobs.snapshot().Log)
	}
	ctx, cancel := context.WithTimeout(s.operationContext(), 25*time.Second)
	snapshot, err := s.readNativeComponents(ctx)
	cancel()
	if err != nil || snapshot.Manifest == nil || !snapshot.Installed || snapshot.Manifest.Revision != manifest.Revision || snapshot.Manifest.Protocol != manifest.Protocol {
		return errors.New("更新后的客机组件未通过实际文件核验，请保留环境并重试同步组件。")
	}
	if err = s.attach(); err != nil {
		return err
	}
	s.mu.Lock()
	s.guestUpdateRequired = false
	s.guestUpdateError = ""
	s.mu.Unlock()
	return s.checkNetworkIfWanted()
}
