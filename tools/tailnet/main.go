// Cage's unprivileged Tailscale node. Run only inside the guest: its normal
// packet path enforces the selected proxy, public UDP denial and network pause.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	gliderssh "github.com/gliderlabs/ssh"
	"github.com/pkg/sftp"
	gossh "golang.org/x/crypto/ssh"
	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
)

const version = "1.104.5"

type settings struct {
	Enabled bool     `json:"enabled"`
	Keys    []string `json:"keys"`
}
type request struct {
	Action string `json:"action"`
	Key    string `json:"key,omitempty"`
	Host   string `json:"host,omitempty"`
	Port   int    `json:"port,omitempty"`
}
type peer struct {
	ID       string     `json:"id,omitempty"`
	Name     string     `json:"name"`
	Hostname string     `json:"hostname,omitempty"`
	OS       string     `json:"os,omitempty"`
	IP       string     `json:"ip"`
	Online   bool       `json:"online"`
	LastSeen *time.Time `json:"last_seen,omitempty"`
}
type state struct {
	Installed bool           `json:"installed"`
	Version   string         `json:"version"`
	Enabled   bool           `json:"enabled"`
	Backend   string         `json:"backend"`
	URL       string         `json:"auth_url,omitempty"`
	Name      string         `json:"name,omitempty"`
	Hostname  string         `json:"hostname,omitempty"`
	OS        string         `json:"os,omitempty"`
	Tailnet   string         `json:"tailnet,omitempty"`
	Account   string         `json:"account,omitempty"`
	Health    []string       `json:"health,omitempty"`
	IPs       []string       `json:"ips"`
	Peers     []peer         `json:"peers"`
	Keys      []string       `json:"keys"`
	User      string         `json:"user"`
	SSHPort   int            `json:"ssh_port"`
	Error     string         `json:"error,omitempty"`
	ProxyURL  string         `json:"proxy_url,omitempty"`
	Forwards  []forwardState `json:"forwards"`
}
type node struct {
	mu          sync.Mutex
	dir         string
	config      settings
	server      *tsnet.Server
	client      *local.Client
	ssh         *gliderssh.Server
	listener    net.Listener
	connections map[net.Conn]bool
	forwards    map[string]*tailnetForward
}

func dataDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "cage-tailnet")
}
func newNode(dir string) (*node, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := protectStateDir(dir); err != nil {
		return nil, err
	}
	n := &node{dir: dir, connections: map[net.Conn]bool{}}
	b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err == nil {
		err = json.Unmarshal(b, &n.config)
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return n, nil
}
func (n *node) save() error {
	b, _ := json.Marshal(n.config)
	p := filepath.Join(n.dir, "settings.json.tmp")
	if err := os.WriteFile(p, b, 0600); err != nil {
		return err
	}
	return os.Rename(p, filepath.Join(n.dir, "settings.json"))
}
func (n *node) start() error {
	if n.server != nil {
		return nil
	}
	hostname, _ := os.Hostname()
	hostname = "cage-" + strings.TrimPrefix(hostname, "cage-")
	s := &tsnet.Server{Dir: n.dir, Hostname: hostname, UserLogf: func(string, ...any) {}}
	return n.startServer(s)
}

func (n *node) startServer(s *tsnet.Server) error {
	if err := s.Start(); err != nil {
		return err
	}
	client, err := s.LocalClient()
	if err != nil {
		s.Close()
		return err
	}
	// Tailscale does not replace guest DNS, accept subnet routes or provide a public exit node.
	_, err = client.EditPrefs(context.Background(), &ipn.MaskedPrefs{Prefs: ipn.Prefs{WantRunning: true, CorpDNS: false, RouteAll: false}, WantRunningSet: true, CorpDNSSet: true, RouteAllSet: true})
	if err != nil {
		s.Close()
		return err
	}
	n.server, n.client = s, client
	listener, err := s.Listen("tcp", ":22")
	if err != nil {
		s.Close()
		n.server, n.client = nil, nil
		return err
	}
	n.listener = listener
	sshServer, err := n.sshServer()
	if err != nil {
		n.stop()
		return err
	}
	n.ssh = sshServer
	go func() { _ = sshServer.Serve(listener) }()
	return nil
}
func (n *node) stop() {
	n.closeForwards()
	if n.ssh != nil {
		_ = n.ssh.Close()
		n.ssh = nil
	}
	if n.listener != nil {
		_ = n.listener.Close()
		n.listener = nil
	}
	for c := range n.connections {
		_ = c.Close()
	}
	clear(n.connections)
	if n.server != nil {
		_ = n.server.Close()
		n.server, n.client = nil, nil
	}
}
func (n *node) snapshot(ctx context.Context) (state, error) {
	username := currentUser()
	out := state{Installed: true, Version: version, Enabled: n.config.Enabled, Backend: "Stopped", IPs: []string{}, Peers: []peer{}, Keys: n.config.Keys, User: username, SSHPort: 22}
	if out.Keys == nil {
		out.Keys = []string{}
	}
	if n.client == nil {
		return out, nil
	}
	status, err := n.client.Status(ctx)
	if err != nil {
		return out, err
	}
	applyStatus(&out, status)
	out.Forwards = n.forwardsSnapshot()
	if out.Backend == "Running" {
		out.ProxyURL = n.proxyURL()
	}
	if err := n.writeSSHConfig(out.Peers); err != nil {
		out.Health = append(out.Health, "SSH 自动配置未完成，私网仍可使用："+err.Error())
	}
	return out, nil
}

func applyStatus(out *state, status *ipnstate.Status) {
	out.Backend, out.URL, out.Health = status.BackendState, status.AuthURL, append([]string(nil), status.Health...)
	if status.CurrentTailnet != nil {
		out.Tailnet = status.CurrentTailnet.Name
	}
	for _, ip := range status.TailscaleIPs {
		out.IPs = append(out.IPs, ip.String())
	}
	if status.Self != nil {
		out.Name = strings.TrimSuffix(status.Self.DNSName, ".")
		out.Hostname, out.OS = status.Self.HostName, status.Self.OS
		if user, ok := status.User[status.Self.UserID]; ok {
			out.Account = user.LoginName
		}
	}
	for _, p := range status.Peer {
		for _, ip := range p.TailscaleIPs {
			if ip.Is4() {
				name := strings.TrimSuffix(p.DNSName, ".")
				if name == "" {
					name = p.HostName
				}
				entry := peer{ID: string(p.ID), Name: name, Hostname: p.HostName, OS: p.OS, IP: ip.String(), Online: p.Online}
				if !p.LastSeen.IsZero() {
					seen := p.LastSeen
					entry.LastSeen = &seen
				}
				out.Peers = append(out.Peers, entry)
				break
			}
		}
	}
	slices.SortFunc(out.Peers, func(a, b peer) int {
		if a.Online != b.Online {
			if a.Online {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
}
func currentUser() string {
	u, err := user.Current()
	if err != nil {
		return "cage"
	}
	return filepath.Base(strings.ReplaceAll(u.Username, `\`, "/"))
}
func (n *node) run(ctx context.Context, r request) (state, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	switch r.Action {
	case "status":
	case "open-service":
		if _, err := n.openForward(ctx, r.Host, r.Port); err != nil {
			return state{}, err
		}
	case "connect", "login":
		if err := n.start(); err != nil {
			return state{}, err
		}
		n.config.Enabled = true
		if err := n.save(); err != nil {
			n.stop()
			return state{}, err
		}
		status, err := n.client.Status(ctx)
		if err != nil {
			return state{}, err
		}
		if r.Action == "login" || !status.HaveNodeKey || status.BackendState == "NeedsLogin" {
			if err := n.client.StartLoginInteractive(ctx); err != nil {
				return state{}, err
			}
		}
	case "disconnect":
		n.config.Enabled = false
		if err := n.save(); err != nil {
			return state{}, err
		}
		n.stop()
		_ = n.writeSSHConfig(nil)
	case "logout":
		if n.client == nil {
			if err := n.start(); err != nil {
				return state{}, err
			}
		}
		if n.client != nil {
			if err := n.client.Logout(ctx); err != nil {
				return state{}, err
			}
		}
		n.config.Enabled = false
		if err := n.save(); err != nil {
			return state{}, err
		}
		n.stop()
		_ = n.writeSSHConfig(nil)
	case "add-key":
		key, comment, options, rest, err := gossh.ParseAuthorizedKey([]byte(strings.TrimSpace(r.Key)))
		if err != nil || len(options) != 0 || len(strings.TrimSpace(string(rest))) != 0 {
			return state{}, errors.New("请粘贴一条有效的 SSH 公钥，不支持私钥或 authorized_keys 限制选项")
		}
		canonical := strings.TrimSpace(string(gossh.MarshalAuthorizedKey(key)))
		if comment != "" {
			canonical += " " + strings.ReplaceAll(comment, "\n", "")
		}
		if len(n.config.Keys) >= 32 {
			return state{}, errors.New("最多保存 32 条 SSH 公钥")
		}
		for _, entry := range n.config.Keys {
			existing, _, _, _, _ := gossh.ParseAuthorizedKey([]byte(entry))
			if existing != nil && string(existing.Marshal()) == string(key.Marshal()) {
				return n.snapshot(ctx)
			}
		}
		n.config.Keys = append(n.config.Keys, canonical)
		if err := n.save(); err != nil {
			return state{}, err
		}
	case "remove-key":
		n.config.Keys = slices.DeleteFunc(n.config.Keys, func(k string) bool { return k == r.Key })
		if err := n.save(); err != nil {
			return state{}, err
		}
	default:
		return state{}, errors.New("Tailscale 操作无效")
	}
	return n.snapshot(ctx)
}
func (n *node) sshServer() (*gliderssh.Server, error) {
	forwarding := &gliderssh.ForwardedTCPHandler{}
	srv := &gliderssh.Server{
		PublicKeyHandler: func(ctx gliderssh.Context, key gliderssh.PublicKey) bool {
			if ctx.User() != currentUser() {
				return false
			}
			n.mu.Lock()
			defer n.mu.Unlock()
			for _, line := range n.config.Keys {
				allowed, _, _, _, err := gossh.ParseAuthorizedKey([]byte(line))
				if err == nil && gliderssh.KeysEqual(key, allowed) {
					return true
				}
			}
			return false
		},
		Handler: func(s gliderssh.Session) { runSession(s) },
		SubsystemHandlers: map[string]gliderssh.SubsystemHandler{"sftp": func(s gliderssh.Session) {
			server, err := sftp.NewServer(s)
			if err != nil {
				_ = s.Exit(1)
				return
			}
			defer server.Close()
			_ = server.Serve()
		}},
		LocalPortForwardingCallback:   func(gliderssh.Context, string, uint32) bool { return true },
		ReversePortForwardingCallback: func(gliderssh.Context, string, uint32) bool { return false },
		ChannelHandlers:               map[string]gliderssh.ChannelHandler{"session": gliderssh.DefaultSessionHandler, "direct-tcpip": gliderssh.DirectTCPIPHandler},
		RequestHandlers:               map[string]gliderssh.RequestHandler{"tcpip-forward": forwarding.HandleSSHRequest, "cancel-tcpip-forward": forwarding.HandleSSHRequest},
		IdleTimeout:                   30 * time.Minute, MaxTimeout: 24 * time.Hour,
	}
	path := filepath.Join(n.dir, "ssh_host_ed25519_key")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		key, err := generateHostKey()
		if err == nil {
			err = os.WriteFile(path, key, 0600)
		}
		if err != nil {
			return nil, err
		}
	}
	if err := srv.SetOption(gliderssh.HostKeyFile(path)); err != nil {
		return nil, err
	}
	return srv, nil
}
func command(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		shell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
		if command == "" {
			return exec.CommandContext(ctx, shell, "-NoLogo")
		}
		return exec.CommandContext(ctx, shell, "-NoLogo", "-NonInteractive", "-Command", command)
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	if command == "" {
		return exec.CommandContext(ctx, shell, "-l")
	}
	return exec.CommandContext(ctx, shell, "-lc", command)
}
func runSession(s gliderssh.Session) {
	cmd := command(s.Context(), s.RawCommand())
	home, _ := os.UserHomeDir()
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	code := runTerminal(s, cmd)
	_ = s.Exit(code)
}
func (n *node) writeSSHConfig(peers []peer) error {
	var b strings.Builder
	if len(peers) > 0 {
		b.WriteString("# Managed by Cage Tailscale. Only known tailnet devices use this transport.\nHost")
		for _, p := range peers {
			b.WriteString(" " + p.IP)
			if safeHost(p.Name) {
				b.WriteString(" " + p.Name)
				short, _, _ := strings.Cut(p.Name, ".")
				if safeHost(short) {
					b.WriteString(" " + short)
				}
			}
		}
		exe, _ := os.Executable()
		exe = filepath.ToSlash(exe)
		b.WriteString("\n  ProxyCommand \"" + strings.ReplaceAll(exe, `"`, `\"`) + "\" dial %h %p\nHost *\n")
	}
	previous, _ := os.ReadFile(filepath.Join(n.dir, "ssh_config"))
	if string(previous) != b.String() {
		if err := os.WriteFile(filepath.Join(n.dir, "ssh_config"), []byte(b.String()), 0600); err != nil {
			return err
		}
	}
	home, _ := os.UserHomeDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return err
	}
	path := filepath.Join(sshDir, "config")
	include := "Include \"" + filepath.ToSlash(filepath.Join(n.dir, "ssh_config")) + "\""
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if !strings.Contains(string(existing), include) {
		return os.WriteFile(path, append([]byte(include+"\n"), existing...), 0600)
	}
	return nil
}
func safeHost(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-') {
			return false
		}
	}
	return true
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "dial" {
		if len(os.Args) != 4 {
			os.Exit(2)
		}
		if err := dial(os.Args[2], os.Args[3]); err != nil {
			fmt.Fprintln(os.Stderr, "Cage Tailscale 连接失败，请检查私网连接与目标 SSH 服务")
			os.Exit(1)
		}
		return
	}
	n, err := newNode(dataDir())
	if err != nil {
		os.Exit(1)
	}
	// Never inherit a host or environment auth key. Enrollment is interactive.
	for _, key := range []string{"TS_AUTHKEY", "TS_AUTH_KEY", "TS_CLIENT_SECRET", "TS_CLIENT_ID", "TSNET_FORCE_LOGIN"} {
		_ = os.Unsetenv(key)
	}
	lock, err := os.OpenFile(filepath.Join(n.dir, "node.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil || lockState(lock) != nil {
		os.Exit(1)
	}
	defer lock.Close()
	if n.config.Enabled {
		_ = n.start()
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		os.Exit(1)
	}
	if err = n.serveDial(listener); err != nil {
		os.Exit(1)
	}
	defer listener.Close()
	defer os.Remove(filepath.Join(n.dir, "control.json"))
	defer func() { n.mu.Lock(); n.stop(); n.mu.Unlock() }()
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_, _ = n.run(ctx, request{Action: "status"})
				cancel()
			}
		}
	}()
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 16384)
	for scanner.Scan() {
		var r request
		var result state
		if err = json.Unmarshal(scanner.Bytes(), &r); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			result, err = n.run(ctx, r)
			cancel()
		}
		if err != nil {
			result.Error = err.Error()
		}
		b, _ := json.Marshal(result)
		fmt.Println(string(b))
	}
}
