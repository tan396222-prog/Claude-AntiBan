package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sylvinus/agent-vm/internal/cage"
)

type claudeDesktopState struct {
	Installed     bool   `json:"installed"`
	Version       string `json:"version"`
	Running       bool   `json:"running"`
	Failed        bool   `json:"failed"`
	Managed       bool   `json:"managed"`
	FailureReason string `json:"failure_reason,omitempty"`
}

const claudeDesktopSnapshot = `python3 - <<'CAGE_DESKTOP_STATE'
import json, subprocess
from pathlib import Path
def run(*args):
    return subprocess.run(args, capture_output=True, text=True).stdout.strip()
package=run('dpkg-query','-W','-f=${Status}\n${Version}','claude-desktop').splitlines()
installed=bool(package and package[0]=='install ok installed')
state=run('systemctl','is-active','cage-claude-desktop.service')
result=run('systemctl','show','--property=Result','--value','cage-claude-desktop.service') if state=='failed' else ''
print(json.dumps({'installed':installed,'version':package[1] if installed else '', 'running':state=='active','failed':state=='failed','managed':Path('/run/cage-desktop/keyring-ready').exists(),'failure_reason':'oom-kill' if result=='oom-kill' else ''}))
CAGE_DESKTOP_STATE`

func (s *Server) claudeDesktopStatus(w http.ResponseWriter, r *http.Request) {
	if !s.vmRunning() {
		http.Error(w, "请先启动 Cage 环境", 409)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	b, err := s.guest(ctx, claudeDesktopSnapshot)
	var state claudeDesktopState
	if err != nil || json.Unmarshal(b, &state) != nil {
		http.Error(w, "无法读取 Claude Desktop 状态，请重试", 502)
		return
	}
	jsonReply(w, state)
}

func (s *Server) claudeDesktopAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	if action != "install" && action != "start" && action != "stop" {
		http.NotFound(w, r)
		return
	}
	var input struct{}
	if !readJSON(w, r, &input) {
		return
	}
	if !s.vmRunning() {
		http.Error(w, "请先启动 Cage 环境", 409)
		return
	}
	s.launch(w, "claude-desktop-"+action, func() error {
		switch action {
		case "install":
			s.mu.Lock()
			allowed := s.toolsNetworkAllowedLocked()
			s.mu.Unlock()
			if !allowed {
				return errors.New("工作台正在退出，请稍后重试安装")
			}
			monitorContext, stopMonitor := context.WithCancel(s.operationContext())
			done := make(chan struct{})
			go func() {
				defer close(done)
				ticker := time.NewTicker(10 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-monitorContext.Done():
						return
					case <-ticker.C:
						s.monitorSafety()
					}
				}
			}()
			defer func() {
				stopMonitor()
				s.mu.Lock()
				if s.monitorCancel != nil {
					s.monitorCancel()
				}
				s.mu.Unlock()
				<-done
			}()
			s.setMessage("正在下载并安装 Claude Desktop，可在操作详情查看进度…")
			return RunMaintenanceScript(s.operationContext(), s.configPath, cage.ClaudeDesktopInstallScript(), s.jobs.execution(), s.jobs.execution())
		case "start":
			s.setMessage("正在打开 Claude Desktop…")
			key, err := s.account.desktopKey()
			if err != nil {
				return err
			}
			defer clear(key)
			script := `test "$(dpkg-query -W -f='${Status}' claude-desktop 2>/dev/null)" = 'install ok installed' || { echo '请先安装 Claude Desktop。'; exit 1; }
` + cage.ClaudeDesktopProvisionScript()
			if err = s.runDesktopScript(script, 20*time.Second); err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(s.operationContext(), 55*time.Second)
			defer cancel()
			cmd := s.sshCommand(ctx, "-T", s.sshTarget(), "--", "/usr/local/bin/cage-claude-start")
			cmd.Stdin = bytes.NewReader(key)
			cmd.Stdout, cmd.Stderr = s.jobs.execution(), s.jobs.execution()
			if err = cmd.Run(); err != nil {
				return errors.New("Claude Desktop 未能启动，请查看操作详情；已有登录数据保持不变")
			}
			return nil
		default:
			s.setMessage("正在关闭 Claude Desktop…")
			return s.runDesktopScript("sudo systemctl stop cage-claude-desktop.service", 30*time.Second)
		}
	})
}

func (s *Server) runDesktopScript(script string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(s.operationContext(), timeout)
	defer cancel()
	// A guest cgroup owns apt and all descendants, including across SSH loss.
	// The application's persistent service is separate from this operation.
	command := fmt.Sprintf("sudo systemd-run --quiet --pipe --wait --collect --unit=cage-desktop-operation --property=User=cage --property=RuntimeMaxSec=%d --property=KillMode=control-group --setenv=HOME=/home/cage --working-directory=/home/cage /bin/bash -se", int(timeout.Seconds()))
	cmd := s.sshCommand(ctx, "-T", s.sshTarget(), "--", command)
	cmd.Stdin = strings.NewReader(script)
	cmd.Stdout, cmd.Stderr = s.jobs.execution(), s.jobs.execution()
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			cleanup, finish := context.WithTimeout(context.Background(), 5*time.Second)
			defer finish()
			_, _ = s.guest(cleanup, "sudo systemctl stop cage-desktop-operation.service")
			return ctx.Err()
		}
		return errors.New("Claude Desktop 操作未完成，请查看操作详情；安装失败可重试，已有浏览器和终端不受影响")
	}
	return nil
}

func (s *Server) claudeDesktopCredentials(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	b, err := s.guest(ctx, "systemctl is-active --quiet cage-claude-desktop.service && cat /home/cage/.config/claude-cage/desktop-password")
	password := strings.TrimSpace(string(b))
	if err != nil || len(password) != 8 {
		http.Error(w, "Claude Desktop 尚未启动", 409)
		return
	}
	jsonReply(w, map[string]string{"password": password})
}

func (s *Server) claudeDesktopResize(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if input.Width < 320 || input.Height < 240 || input.Width > 8192 || input.Height > 8192 {
		http.Error(w, "画面尺寸无效", 400)
		return
	}
	// Fixed commands and validated integers only; no host shell executes input.
	wid, hei := input.Width, input.Height
	script := fmt.Sprintf(`set -eu
export DISPLAY=:78 XAUTHORITY=/run/cage-desktop/Xauthority
exec 9>/run/cage-desktop/resize.lock
flock 9
name=cage-%dx%d
if ! xrandr --query | grep -q " $name "; then
  xrandr --newmode "$name" %.3f %d %d %d %d %d %d %d %d +HSync -VSync
  xrandr --addmode VNC-0 "$name"
fi
xrandr --output VNC-0 --mode "$name"
for old in $(xrandr --query | awk '/^ +cage-/ {print $1}'); do
  if [ "$old" != "$name" ]; then xrandr --delmode VNC-0 "$old"; xrandr --rmmode "$old"; fi
done`, wid, hei, float64((wid+32)*(hei+8)*60)/1000000, wid, wid+8, wid+16, wid+32, hei, hei+1, hei+2, hei+8)
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if _, err := s.guest(ctx, script); err != nil {
		http.Error(w, "Claude Desktop 画面尺寸未能调整", 502)
		return
	}
	jsonReply(w, map[string]bool{"ok": true})
}

func (s *Server) claudeDesktopVNC(w http.ResponseWriter, r *http.Request) {
	if !s.vmRunning() {
		http.Error(w, "环境尚未启动", 409)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	// No extra localhost port or public VNC listener on the host. SSH is bound
	// to this profile's single guest and carries the authenticated RFB session.
	cmd := s.sshCommand(ctx, "-T", s.sshTarget(), "--", "exec socat STDIO UNIX-CONNECT:/run/cage-desktop/vnc.sock")
	reader, err := cmd.StdoutPipe()
	if err != nil {
		http.Error(w, "无法连接应用画面", 502)
		return
	}
	writer, err := cmd.StdinPipe()
	if err != nil {
		_ = reader.Close()
		http.Error(w, "无法连接应用画面", 502)
		return
	}
	if err = cmd.Start(); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		http.Error(w, "无法连接应用画面", 502)
		return
	}
	defer func() { cancel(); writer.Close(); reader.Close(); _ = cmd.Wait() }()
	s.streamVNC(w, r, reader, writer, func() { cancel(); _ = writer.Close(); _ = reader.Close() })
}
