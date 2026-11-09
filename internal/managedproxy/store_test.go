package managedproxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sylvinus/agent-vm/internal/host"
	"go.yaml.in/yaml/v3"
)

func TestCumulativeImportsPreserveReadableStoreOnOverflow(t *testing.T) {
	profile := t.TempDir()
	s, err := Load(profile)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("proxies:\n  - name: synthetic\n    type: socks5\n    server: example.com\n    port: 1080\n    password: " + strings.Repeat("x", 7<<20) + "\n")
	for i := 0; i < 4; i++ {
		if err := s.Import(fmt.Sprintf("source-%d", i), "", data, "", 0, ""); err != nil {
			t.Fatal(err)
		}
		if err := s.Save(profile); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(Dir(profile), "sources.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Import("overflow", "", data, "", 0, ""); err != nil {
		t.Fatal("individual import should remain within its input limit", err)
	}
	if err := s.Save(profile); err == nil {
		t.Fatal("cumulative imports saved an unreadable store")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed save changed the previous file", err)
	}
	loaded, err := Load(profile)
	if err != nil || len(loaded.Sources) != 4 || loaded.Auth != s.Auth {
		t.Fatal("previous sources could not be recovered after rejected import", err)
	}
	// Normal source removal must still work after the rejected import.
	loaded.Sources = loaded.Sources[:3]
	if err := loaded.Save(profile); err != nil {
		t.Fatal(err)
	}
	if loaded, err := Load(profile); err != nil || len(loaded.Sources) != 3 {
		t.Fatal("source removal failed after rejected import", err)
	}
}

func TestStoreLimitCoversSerializedBytesAndAllowsExactLimit(t *testing.T) {
	profile := t.TempDir()
	s, _ := Load(profile)
	if err := s.Import("source", "", []byte(testNodes), "source", 0, ""); err != nil {
		t.Fatal(err)
	}
	s.Sources[0].Nodes[0].Config["password"] = ""
	if err := s.Save(profile); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(Dir(profile), "sources.json")
	base, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	padding := strings.Repeat("x", maxStoreSize-int(base.Size()))
	s.Sources[0].Nodes[0].Config["password"] = padding
	if err := s.Save(profile); err != nil {
		t.Fatal("exact limit was rejected", err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() != maxStoreSize {
		t.Fatal("fixture did not reach the exact serialized limit", err)
	}
	if _, err := Load(profile); err != nil {
		t.Fatal("exact-size saved store was not readable", err)
	}
	// The raw string has the same byte length, but JSON escapes '<' into six
	// bytes. Limits must cover the persisted representation, not input lengths.
	s.Sources[0].Nodes[0].Config["password"] = padding[:len(padding)-1] + "<"
	if err := s.Save(profile); err == nil {
		t.Fatal("JSON expansion bypassed the persisted-size limit")
	}
	loaded, err := Load(profile)
	if err != nil || loaded.Sources[0].Nodes[0].Config["password"] != padding {
		t.Fatal("overflow replaced the exact-size previous store", err)
	}
}

const testNodes = `mixed-port: 7890
external-controller: 0.0.0.0:9090
tun: {enable: true}
rules: [MATCH,DIRECT]
proxies:
  - {name: edge, type: socks5, server: 127.0.0.1, port: 1080, password: private-secret}
  - {name: chained, type: http, server: 127.0.0.1, port: 8080, dialer-proxy: edge}
`

func TestImportCompilesOnlyOwnedNetworkBoundary(t *testing.T) {
	s, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Import("test", "", []byte(testNodes), "source", 0, ""); err != nil {
		t.Fatal(err)
	}
	s.Selected = s.Sources[0].Nodes[1].ID
	b, err := s.Runtime("/tmp/qa")
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if yaml.Unmarshal(b, &config) != nil {
		t.Fatal("invalid runtime")
	}
	if config["mixed-port"] != 17896 || config["allow-lan"] != false || config["ipv6"] != false || config["external-controller"] != nil {
		t.Fatalf("import changed boundary: %v", config)
	}
	if !strings.Contains(string(b), "MATCH,CAGE") || strings.Contains(string(b), "DIRECT") || strings.Contains(string(b), "enable: true\nproxies:") {
		t.Fatal("unsafe imported routing")
	}
	if config["tun"].(map[string]any)["enable"] != false {
		t.Fatal("TUN enabled")
	}
	if config["unified-delay"] != true {
		t.Fatal("node latency includes connection setup instead of an established-connection request")
	}
	if s.Sources[0].Nodes[1].Config["dialer-proxy"] != s.Sources[0].Nodes[0].ID {
		t.Fatal("chain not namespaced")
	}
	profile := t.TempDir()
	if err = s.Save(profile); err != nil {
		t.Fatal(err)
	}
	if err := host.CheckPrivateFile(filepath.Join(Dir(profile), "sources.json")); err != nil {
		t.Fatalf("source credentials not private: %v", err)
	}
	loaded, err := Load(profile)
	if err != nil || loaded.Endpoint() != s.Endpoint() {
		t.Fatal("credentials not stable")
	}
}
func TestImportRejectsFilesDirectAndCycles(t *testing.T) {
	for _, nodes := range []string{
		`proxies: [{name: test, type: http, server: example.com, port: 80, interface-name: en0}]`,
		`proxies: [{name: test, type: socks5, server: example.com, port: 80, dialer-proxy: DIRECT}]`,
		`proxies: [{name: test, type: socks5, server: example.com, port: 80, dialer-proxy: test}]`,
		`proxies: [{name: test, type: http, server: example.com, port: 80, ws-opts: {private-key: /etc/passwd}}]`,
		`proxies: [{name: test, type: mieru, server: example.com, port: 443, transport: TCP, username: test-user, password: test-password, interface-name: en0}]`,
		`proxies: [{name: test, type: mieru, server: example.com, port: 443, transport: TCP, username: test-user, password: test-password, tls-opts: {ca: /etc/passwd}}]`,
		`proxies: [{name: duplicate, type: http, server: a, port: 80}, {name: duplicate, type: http, server: b, port: 80}]`,
	} {
		if _, err := Parse([]byte(nodes), "test"); err == nil {
			t.Fatal("unsafe import accepted")
		}
	}
}

func TestAnyTLSIdleSessionOptionsRemainCompatible(t *testing.T) {
	_, err := Parse([]byte(`proxies: [{name: qa, type: anytls, server: example.com, port: 443, password: secret, idle-session-check-interval: 30, idle-session-timeout: 30, min-idle-session: 0}]`), "source")
	if err != nil {
		t.Fatal(err)
	}
}

func TestMieruMixedSubscription(t *testing.T) {
	// Match the reported subscription's mix without using account credentials.
	var data strings.Builder
	data.WriteString("proxies:\n")
	for i := 0; i < 113; i++ {
		fmt.Fprintf(&data, "  - {name: vless-%d, type: vless, server: example.com, port: 443, uuid: 00000000-0000-4000-8000-000000000001}\n", i)
	}
	for i := 0; i < 19; i++ {
		fmt.Fprintf(&data, "  - {name: mieru-%d, type: mieru, server: example.com, port: 443, username: test-user, password: test-password, transport: TCP, udp: true}\n", i)
	}
	profile := t.TempDir()
	s, err := Load(profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Import("mixed", "", []byte(data.String()), "source", 24, ""); err != nil {
		t.Fatal(err)
	}
	if s.Count() != 132 {
		t.Fatalf("imported %d nodes, want 132", s.Count())
	}
	s.Selected = s.Sources[0].Nodes[113].ID
	if err := s.Save(profile); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(profile)
	if err != nil {
		t.Fatal(err)
	}
	node, ok := loaded.Find(s.Selected)
	if !ok || node.Type != "mieru" || node.Config["transport"] != "TCP" || node.Config["username"] != "test-user" || node.Config["password"] != "test-password" || node.Config["udp"] != false {
		t.Fatal("Mieru options or Cage UDP boundary lost during persistence")
	}
	other, err := Parse([]byte(data.String()), "source")
	if err != nil || other[113].ID != node.ID {
		t.Fatal("refresh changed Mieru node identity", err)
	}
	checkMieruRuntime(t, profile, loaded)
}

func TestMieruTransportOptions(t *testing.T) {
	for _, root := range []string{"proxies", "payload"} {
		t.Run(root, func(t *testing.T) {
			s, err := Load(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			data := root + `: [{name: mieru-udp, type: mieru, server: example.com, port-range: 8000-8010, username: test-user, password: test-password, transport: UDP, multiplexing: MULTIPLEXING_LOW, handshake-mode: HANDSHAKE_STANDARD}]`
			if err := s.Import("mieru", "", []byte(data), "source", 0, ""); err != nil {
				t.Fatal(err)
			}
			s.Selected = s.Sources[0].Nodes[0].ID
			checkMieruRuntime(t, t.TempDir(), s)
		})
	}
}

func checkMieruRuntime(t *testing.T, profile string, s Store) {
	t.Helper()
	b, err := s.Runtime(profile)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(b, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Proxies) != s.Count() {
		t.Fatal("runtime lost imported nodes")
	}
	for _, node := range config.Proxies {
		if node["type"] == "mieru" && (node["transport"] == nil || node["udp"] != false) {
			t.Fatal("runtime lost Mieru transport or enabled guest UDP")
		}
	}
	// Run the compiled test binary beside the bundled mihomo to exercise the
	// same candidate-config validation used by subscription import and refresh.
	if os.Getenv("CAGE_PROXY_INTEGRATION") == "1" {
		if err := Validate(context.Background(), profile, s); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRefreshIdentityAndInvalidUpdatePreserveNodes(t *testing.T) {
	s, _ := Load(t.TempDir())
	if err := s.Import("test", "https://example.com/sub?secret=x", []byte(testNodes), "source", 24, ""); err != nil {
		t.Fatal(err)
	}
	id := s.Sources[0].Nodes[0].ID
	if err := s.Import("test", "", []byte("not: a subscription"), "source", 24, ""); err == nil {
		t.Fatal("bad source accepted")
	}
	if s.Sources[0].Nodes[0].ID != id {
		t.Fatal("failed update lost nodes")
	}
	if err := s.Import("test", "", []byte(testNodes), "source", 24, ""); err != nil || s.Sources[0].Nodes[0].ID != id {
		t.Fatal("refresh changed node identity")
	}
	other, _ := Parse([]byte(testNodes), "other")
	if other[0].ID == id {
		t.Fatal("sources collide")
	}
}
func TestSubscriptionFetchAndMetadata(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.UserAgent() != "clash.meta/Cage" {
			t.Error("incorrect subscription user agent")
		}
		w.Header().Set("Subscription-Userinfo", "upload=2; download=3; total=100; expire=123; secret=credential")
		io.WriteString(w, testNodes)
	}))
	defer server.Close()
	data, usage, err := Fetch(context.Background(), server.Client(), server.URL)
	if err != nil || string(data) != testNodes || usage != "upload=2; download=3; total=100; expire=123" {
		t.Fatalf("fetch: %s %v", usage, err)
	}
	if _, _, err = Fetch(context.Background(), server.Client(), "http://example.com"); err == nil {
		t.Fatal("insecure subscription accepted")
	}
}
func TestBootstrapDoesNotAllowPrivateDestinations(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "198.18.0.1", "::1"} {
		if publicIP(netip.MustParseAddr(ip)) {
			t.Fatal("infrastructure private destination allowed")
		}
	}
}
