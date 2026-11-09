// Package managedproxy owns Cage's private proxy sources and generated core configuration.
package managedproxy

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sylvinus/agent-vm/internal/host"
	"go.yaml.in/yaml/v3"
)

const Group = "CAGE"
const MaxImport = 8 << 20
const maxStoreSize = 32 << 20

type Node struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Type   string         `json:"type"`
	Config map[string]any `json:"config"`
}
type Source struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url,omitempty"`
	Interval  int       `json:"interval_hours"`
	Updated   time.Time `json:"updated_at"`
	Attempted time.Time `json:"attempted_at"`
	Error     string    `json:"error,omitempty"`
	Usage     string    `json:"usage,omitempty"`
	Nodes     []Node    `json:"nodes"`
}
type Store struct {
	Version  int      `json:"version"`
	Port     int      `json:"port"`
	Auth     string   `json:"auth"`
	Revision string   `json:"revision"`
	Selected string   `json:"selected"`
	Chain    []string `json:"chain,omitempty"`
	Sources  []Source `json:"sources"`
}

func Token() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func Dir(profile string) string { return filepath.Join(profile, "proxy") }
func Socket(profile string) string {
	h := sha256.Sum256([]byte(filepath.Clean(profile)))
	if runtime.GOOS == "windows" {
		return filepath.Join(os.TempDir(), fmt.Sprintf("cage-proxy-%x", h[:8]), "control.sock")
	}
	return filepath.Join("/tmp", fmt.Sprintf("cage-proxy-%d-%x", os.Getuid(), h[:8]), "control.sock")
}
func Load(profile string) (Store, error) {
	var s Store
	f, err := os.Open(filepath.Join(Dir(profile), "sources.json"))
	if os.IsNotExist(err) {
		return Store{Version: 1, Port: 17896, Auth: Token(), Sources: []Source{}}, nil
	}
	if err != nil {
		return s, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxStoreSize+1))
	if err != nil {
		return s, err
	}
	if len(b) > maxStoreSize || json.Unmarshal(b, &s) != nil || s.Version != 1 || s.Port < 1024 || s.Port > 65535 || len(s.Auth) != 48 {
		return s, errors.New("Cage 代理配置无效，请恢复私有配置备份")
	}
	return s, nil
}
func Write(path string, data []byte) error {
	if err := host.EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".proxy-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = host.ProtectFile(f.Name()); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func (s Store) Save(profile string) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if len(b) > maxStoreSize {
		return errors.New("代理来源配置总大小超过 32 MiB，请减少来源或节点；原配置已保留")
	}
	return Write(filepath.Join(Dir(profile), "sources.json"), b)
}
func (s Store) Endpoint() string { return fmt.Sprintf("socks5://cage:%s@127.0.0.1:%d", s.Auth, s.Port) }
func (s Store) Find(id string) (Node, bool) {
	for _, src := range s.Sources {
		for _, n := range src.Nodes {
			if n.ID == id {
				return n, true
			}
		}
	}
	return Node{}, false
}
func (s Store) Count() int {
	n := 0
	for _, src := range s.Sources {
		n += len(src.Nodes)
	}
	return n
}
func (s *Store) Import(name, rawURL string, data []byte, id string, interval int, usage string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return errors.New("请填写 1–100 字节的来源名称")
	}
	if interval < 0 || interval > 168 {
		return errors.New("更新间隔应为 0–168 小时，0 表示手动更新")
	}
	if id == "" {
		id = Token()[:16]
	}
	nodes, err := Parse(data, id)
	if err != nil {
		return err
	}
	src := Source{ID: id, Name: name, URL: rawURL, Interval: interval, Updated: time.Now(), Attempted: time.Now(), Nodes: nodes, Usage: usage}
	for i := range s.Sources {
		if s.Sources[i].ID == id {
			s.Sources[i] = src
			return nil
		}
	}
	s.Sources = append(s.Sources, src)
	return nil
}

// Node-only import keeps remote files from changing listeners, routes, or file paths.
func Parse(data []byte, sourceID string) ([]Node, error) {
	if len(data) == 0 || len(data) > MaxImport {
		return nil, errors.New("配置为空或超过 8 MiB")
	}
	var input struct {
		Proxies []map[string]any `yaml:"proxies"`
		Payload []map[string]any `yaml:"payload"`
	}
	if yaml.Unmarshal(data, &input) != nil {
		return nil, errors.New("无法读取 YAML，请导入 Clash/Mihomo 节点订阅或配置")
	}
	proxies := input.Proxies
	if len(proxies) == 0 {
		proxies = input.Payload
	}
	if len(proxies) == 0 || len(proxies) > 3000 {
		return nil, errors.New("配置需要包含 1–3000 个 proxies 节点；仅含远程 provider 的配置请改为导入节点订阅链接")
	}
	allowed := strings.Fields("name type server port password username cipher uuid alterId network tls udp skip-cert-verify servername sni alpn client-fingerprint fingerprint ws-opts grpc-opts http-opts h2-opts reality-opts packet-encoding flow encryption obfs obfs-password auth auth-str up down ports hop-interval recv-window-conn recv-window disable-mtu-discovery fast-open protocol protocol-param obfs-param plugin plugin-opts dialer-proxy smux ip-version tfo mptcp certificate-fingerprint idle-session-check-interval idle-session-timeout min-idle-session global-padding authenticated-length heartbeat-interval reduce-rtt request-timeout max-open-streams max-udp-relay-packet-size congestion-controller udp-relay-mode zero-rtt udp-over-stream udp-over-tcp udp-over-tcp-version ech-opts client-id token transport port-range multiplexing handshake-mode traffic-pattern")
	keys := map[string]bool{}
	for _, k := range allowed {
		keys[k] = true
	}
	types := map[string]bool{}
	for _, k := range strings.Fields("http socks5 ss ssr vmess vless trojan hysteria hysteria2 tuic anytls mieru") {
		types[k] = true
	}
	names := map[string]string{}
	out := make([]Node, 0, len(proxies))
	for _, p := range proxies {
		name, _ := p["name"].(string)
		typ, _ := p["type"].(string)
		server, _ := p["server"].(string)
		if strings.TrimSpace(name) == "" || len(name) > 240 || names[name] != "" || !types[typ] || server == "" || len(server) > 253 {
			return nil, errors.New("节点名称重复、服务器缺失或协议暂不支持；支持 HTTP/SOCKS5/SS/SSR/VMess/VLESS/Trojan/Hysteria/TUIC/AnyTLS/Mieru")
		}
		for k := range p {
			if !keys[k] {
				return nil, fmt.Errorf("节点包含不支持的字段 %q，请使用标准节点订阅", k)
			}
		}
		// Nested TLS file references are not accepted even inside transport options.
		if unsafeOption(p) {
			return nil, errors.New("节点不能引用本机文件、网络接口或额外路由")
		}
		h := sha256.Sum256([]byte(sourceID + "\x00" + name))
		id := hex.EncodeToString(h[:12])
		names[name] = id
		out = append(out, Node{ID: id, Name: name, Type: typ, Config: p})
	}
	for i := range out {
		p := out[i].Config
		p["name"] = out[i].ID
		if v, exists := p["dialer-proxy"]; exists {
			target, ok := v.(string)
			if !ok || names[target] == "" {
				return nil, errors.New("代理链必须引用同一来源中的具体节点，不能引用 DIRECT 或选择分组")
			}
			p["dialer-proxy"] = names[target]
		}
		p["udp"] = false
	}
	byID := map[string]map[string]any{}
	for _, n := range out {
		byID[n.ID] = n.Config
	}
	for _, n := range out {
		seen := map[string]bool{}
		id := n.ID
		for id != "" {
			if seen[id] {
				return nil, errors.New("代理链存在循环")
			}
			seen[id] = true
			id, _ = byID[id]["dialer-proxy"].(string)
		}
	}
	return out, nil
}
func unsafeOption(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			if strings.Contains(k, "path") || k == "ca" || k == "certificate" || k == "private-key" || k == "interface-name" || k == "routing-mark" {
				if k != "path" {
					return true
				}
			}
			if unsafeOption(v) {
				return true
			}
		}
	case []any:
		for _, v := range x {
			if unsafeOption(v) {
				return true
			}
		}
	}
	return false
}

// Route returns the actual hops from entry to exit. Explicit UI chains override
// dialers at runtime without modifying subscription nodes or their credentials.
func (s Store) Route() ([]string, error) {
	if _, ok := s.Find(s.Selected); !ok {
		return nil, errors.New("请先选择有效的最终出口")
	}
	if len(s.Chain) > 0 {
		if s.Chain[len(s.Chain)-1] != s.Selected {
			return nil, errors.New("代理链与最终出口不一致，请重新配置代理")
		}
		seen := map[string]bool{}
		for _, id := range s.Chain {
			if _, ok := s.Find(id); !ok {
				return nil, errors.New("代理链中的节点已被移除，请重新选择；原配置已保留")
			}
			if seen[id] {
				return nil, errors.New("代理链不能重复使用同一个节点")
			}
			seen[id] = true
		}
		return append([]string(nil), s.Chain...), nil
	}
	var reversed []string
	seen := map[string]bool{}
	for id := s.Selected; id != ""; {
		n, ok := s.Find(id)
		if !ok || seen[id] {
			return nil, errors.New("导入的代理链存在循环或缺失节点，请检查来源配置")
		}
		seen[id] = true
		reversed = append(reversed, id)
		id, _ = n.Config["dialer-proxy"].(string)
	}
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	return reversed, nil
}

func (s Store) Runtime(profile string) ([]byte, error) {
	if _, err := s.Route(); err != nil {
		return nil, err
	}
	dialers := map[string]string{}
	for i, id := range s.Chain {
		dialers[id] = ""
		if i > 0 {
			dialers[id] = s.Chain[i-1]
		}
	}
	proxies := []map[string]any{}
	for _, src := range s.Sources {
		for _, n := range src.Nodes {
			config := make(map[string]any, len(n.Config))
			for key, value := range n.Config {
				config[key] = value
			}
			if via, overridden := dialers[n.ID]; overridden {
				delete(config, "dialer-proxy")
				if via != "" {
					config["dialer-proxy"] = via
				}
			}
			proxies = append(proxies, config)
		}
	}
	// Infrastructure DNS bootstraps only the core's server domains. Guest DNS
	// remains behind Cage Egress, and there is no host-system resolver fallback.
	dns := map[string]any{"enable": true, "ipv6": false, "use-hosts": false, "use-system-hosts": false, "default-nameserver": BootstrapDNSURLs(), "proxy-server-nameserver": BootstrapDNSURLs(), "nameserver": []string{"https://1.1.1.1/dns-query#" + Group}}
	config := map[string]any{"mixed-port": s.Port, "bind-address": "127.0.0.1", "allow-lan": false, "authentication": []string{"cage:" + s.Auth}, "skip-auth-prefixes": []string{}, "mode": "rule", "ipv6": false, "log-level": "warning", "find-process-mode": "off", "external-controller-unix": Socket(profile), "tun": map[string]any{"enable": false}, "dns": dns, "geo-auto-update": false, "profile": map[string]any{"store-selected": false}, "proxies": proxies, "proxy-groups": []any{map[string]any{"name": Group, "type": "select", "proxies": []string{s.Selected}}}, "rules": []string{"MATCH," + Group}}
	// Measure a request on the established connection rather than folding
	// protocol and TLS setup into the node's displayed HTTP latency.
	config["unified-delay"] = true
	return yaml.Marshal(config)
}
