package workbench

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"al.essio.dev/pkg/shellescape"

	"github.com/sylvinus/agent-vm/internal/cage"
)

// Only distribution/tool origins are reachable through this temporary channel.
// Guest business traffic continues through EgressGate without any grant here.
var maintenanceOrigins = map[string]bool{
	"deb.debian.org": true, "security.debian.org": true,
	"github.com": true, "objects.githubusercontent.com": true, "release-assets.githubusercontent.com": true,
	"registry.npmjs.org": true, "downloads.claude.ai": true, "deb.nodesource.com": true,
}

type maintenanceProxy struct {
	token       string
	ctx         context.Context
	dial        func(context.Context, string, string) (net.Conn, error)
	transport   *http.Transport
	mu          sync.Mutex
	connections map[net.Conn]bool
	closed      bool
}

func maintenanceDestination(address string, connect bool) bool {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if connect && port != "443" {
		return false
	}
	if !connect && port != "80" {
		return false
	}
	host = strings.ToLower(host)
	switch host {
	case "mirrors.nju.edu.cn", "mirrors.ustc.edu.cn", "cdn.npmmirror.com":
		return connect
	}
	return maintenanceOrigins[host]
}
func (p *maintenanceProxy) track(conn net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		conn.Close()
		return false
	}
	p.connections[conn] = true
	return true
}
func (p *maintenanceProxy) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	for conn := range p.connections {
		conn.Close()
	}
	p.transport.CloseIdleConnections()
}
func (p *maintenanceProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("cage-install:"+p.token))
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Proxy-Authorization")), []byte(expected)) != 1 {
		w.Header().Set("Proxy-Authenticate", `Basic realm="Cage maintenance"`)
		http.Error(w, "Maintenance authentication required", 407)
		return
	}
	if r.Method == http.MethodConnect {
		if !maintenanceDestination(r.Host, true) {
			http.Error(w, "Not a Cage software source", 403)
			return
		}
		remote, err := p.dial(p.ctx, "tcp", r.Host)
		if err != nil {
			http.Error(w, "Software source proxy unavailable", 502)
			return
		}
		if !p.track(remote) {
			return
		}
		defer func() { remote.Close(); p.mu.Lock(); delete(p.connections, remote); p.mu.Unlock() }()
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "Tunnel unavailable", 500)
			return
		}
		local, buffer, err := hijacker.Hijack()
		if err != nil {
			return
		}
		if !p.track(local) {
			return
		}
		defer func() { local.Close(); p.mu.Lock(); delete(p.connections, local); p.mu.Unlock() }()
		if _, err = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if err = buffer.Flush(); err != nil {
			return
		}
		done := make(chan struct{})
		go func() { _, _ = io.Copy(remote, buffer); remote.Close(); close(done) }()
		_, _ = io.Copy(local, remote)
		local.Close()
		remote.Close()
		<-done
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Read-only software source", 405)
		return
	}
	address := r.URL.Host
	if r.URL.Port() == "" {
		address = net.JoinHostPort(r.URL.Hostname(), "80")
	}
	if r.URL.Scheme != "http" || r.URL.User != nil || !maintenanceDestination(address, false) {
		http.Error(w, "Not a Cage software source", 403)
		return
	}
	request := r.Clone(p.ctx)
	request.RequestURI = ""
	request.Header = r.Header.Clone()
	request.Header.Del("Proxy-Authorization")
	request.Header.Del("Proxy-Connection")
	response, err := p.transport.RoundTrip(request)
	if err != nil {
		http.Error(w, "Software source proxy unavailable", 502)
		return
	}
	defer response.Body.Close()
	for name, values := range response.Header {
		if strings.EqualFold(name, "Connection") {
			continue
		}
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}

const maintenancePort = 18961

// The script and one-use local credential travel on SSH stdin, never argv.
// APT_CONFIG is passed explicitly through sudo by the owned install scripts.
func maintenanceScript(script, token string) string {
	return fmt.Sprintf(`set -euo pipefail
umask 077
cage_maintenance_dir=$(mktemp -d)
trap 'rm -rf "$cage_maintenance_dir"' EXIT
export CAGE_INSTALL_PROXY='http://cage-install:%s@127.0.0.1:%d'
export CAGE_APT_CONFIG="$cage_maintenance_dir/apt.conf"
printf 'Acquire::http::Proxy "%%s";\nAcquire::https::Proxy "%%s";\nAcquire::Retries "3";\nAcquire::http::Timeout "60";\nAcquire::https::Timeout "60";\n' "$CAGE_INSTALL_PROXY" "$CAGE_INSTALL_PROXY" > "$CAGE_APT_CONFIG"
unset ALL_PROXY all_proxy NO_PROXY no_proxy FTP_PROXY ftp_proxy
export http_proxy="$CAGE_INSTALL_PROXY" https_proxy="$CAGE_INSTALL_PROXY" HTTP_PROXY="$CAGE_INSTALL_PROXY" HTTPS_PROXY="$CAGE_INSTALL_PROXY"
(
umask 022
%s
)
`, token, maintenancePort, script)
}

func RunMaintenanceScript(ctx context.Context, configPath, script string, stdout, stderr io.Writer) error {
	cfg, err := cage.Load(configPath)
	if err != nil {
		return err
	}
	profile := filepath.Dir(configPath)
	if err = cfg.RequireManagedProxy(profile); err != nil {
		return err
	}
	egress, err := cage.NewEgress(cfg)
	if err != nil {
		return err
	}
	// Each download detects inactivity; cancellation still closes forwarding
	// and stops the guest operation without imposing a total install duration.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	proxy := &maintenanceProxy{token: randomToken(), ctx: ctx, dial: egress.DialContext, connections: make(map[net.Conn]bool)}
	proxy.transport = &http.Transport{Proxy: nil, DialContext: egress.DialContext, DisableKeepAlives: true}
	server := &http.Server{Handler: proxy, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	defer proxy.close()
	stop := context.AfterFunc(ctx, func() { _ = server.Close(); proxy.close() })
	defer stop()
	s := &Server{profile: profile}
	forward := fmt.Sprintf("127.0.0.1:%d:%s", maintenancePort, listener.Addr().String())
	command := "sudo systemd-run --quiet --pipe --wait --collect --unit=cage-maintenance-operation --property=User=cage --property=RuntimeMaxSec=infinity --property=KillMode=control-group --setenv=HOME=/home/cage --working-directory=" + shellescape.Quote(guestPath(s.environmentDir())) + " /bin/bash -se"
	cmd := s.sshCommandWithForwarding(ctx, "-o", "ExitOnForwardFailure=yes", "-R", forward, "-T", s.sshTarget(), "--", command)
	cmd.Stdin = strings.NewReader(maintenanceScript(cage.DebianMirrorsScript+"\n"+script, proxy.token))
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			cleanup, finish := context.WithTimeout(context.Background(), 5*time.Second)
			defer finish()
			_, _ = s.guest(cleanup, "sudo systemctl stop cage-maintenance-operation.service")
			return ctx.Err()
		}
		return errors.New("依赖安装通道未完成，请检查已选代理与操作详情；业务网络状态保持不变")
	}
	return nil
}
