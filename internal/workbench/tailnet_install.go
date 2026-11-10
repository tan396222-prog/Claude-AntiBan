package workbench

import (
	"al.essio.dev/pkg/shellescape"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"github.com/sylvinus/agent-vm/internal/tailnetassets"
	"io"
	"strings"
	"time"
)

// Stage beside the installed executable so a running node can finish on its old
// inode. Identity and user data live elsewhere and are never part of installation.
const tailnetInstallScript = `set -eu; tmp=$(mktemp /tmp/cage-tailnet.XXXXXX); stage=; trap 'rm -f "$tmp"; if test -n "$stage"; then sudo rm -f "$stage"; fi' EXIT; cat > "$tmp"; stage=$(sudo mktemp /usr/local/bin/.cage-tailnet.XXXXXX); sudo install -m 0755 "$tmp" "$stage"; sudo mv -f "$stage" /usr/local/bin/cage-tailnet; printf 'cage-browser ALL=(cage) NOPASSWD: /usr/local/bin/cage-tailnet\n' | sudo tee /etc/sudoers.d/cage-tailnet >/dev/null; sudo chmod 0440 /etc/sudoers.d/cage-tailnet`

func (s *Server) refreshInstalledTailnet() error {
	ctx, cancel := context.WithTimeout(s.operationContext(), 15*time.Second)
	defer cancel()
	installed, err := s.guest(ctx, "if test -x /usr/local/bin/cage-tailnet; then printf installed; fi")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(installed)) == "installed" {
		return s.installTailnet()
	}
	return nil
}

func (s *Server) installTailnet() error {
	if s.macosGuest != nil || s.windowsGuest != nil {
		return errTailnetNativeInstall
	}
	if !s.vmRunning() {
		return errors.New("请先启动 Cage 环境")
	}
	ctx, cancel := context.WithTimeout(s.operationContext(), 2*time.Minute)
	defer cancel()
	arch, err := s.guest(ctx, "uname -m")
	if err != nil {
		return err
	}
	name := "amd64"
	if string(bytes.TrimSpace(arch)) == "aarch64" {
		name = "arm64"
	} else if string(bytes.TrimSpace(arch)) != "x86_64" {
		return errors.New("当前客机架构尚不支持 Tailscale")
	}
	payload, err := tailnetassets.Linux(name)
	if err != nil {
		return errors.New("此构建缺少 Tailscale 客机组件，请安装完整的 Cage 构建")
	}
	defer payload.Close()
	reader, err := gzip.NewReader(payload)
	if err != nil {
		return err
	}
	defer reader.Close()
	cmd := s.sshCommand(ctx, "-T", s.sshTarget(), "--", "/bin/bash -c "+shellescape.Quote(tailnetInstallScript))
	cmd.Stdin = reader
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	s.setMessage("正在安装 Tailscale，已有终端和浏览器保持运行…")
	if err := cmd.Run(); err != nil {
		return errors.New("Tailscale 安装未完成，请检查客机连接后重试")
	}
	return nil
}
