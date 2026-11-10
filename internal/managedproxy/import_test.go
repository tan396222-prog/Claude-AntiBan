package managedproxy

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestHysteria2SubscriptionAliasesPreserveTransportAndPin(t *testing.T) {
	profile := t.TempDir()
	s, _ := Load(profile)
	pin := strings.Repeat("ab", 32)
	data := `proxies:
 - {name: ordinary, type: socks5, server: 127.0.0.1, port: 1080}
 - {name: hopping, type: hysteria2, server: example.com, port: 35000, password: test-password, mport: 35000-39000, pinSHA256: "` + pin + `"}
`
	if err := s.Import("feedback", "", []byte(data), "feedback", 0, ""); err != nil {
		t.Fatal(err)
	}
	if s.Count() != 2 {
		t.Fatalf("imported %d nodes, want 2", s.Count())
	}
	s.Selected = s.Sources[0].Nodes[1].ID
	runtime, err := s.Runtime(profile)
	if err != nil {
		t.Fatal(err)
	}
	var config struct{ Proxies []map[string]any }
	if err := yaml.Unmarshal(runtime, &config); err != nil {
		t.Fatal(err)
	}
	hy2 := config.Proxies[1]
	if hy2["ports"] != "35000-39000" || hy2["fingerprint"] != pin || hy2["mport"] != nil || hy2["pinSHA256"] != nil {
		t.Fatalf("aliases did not preserve transport and certificate pin: %v", hy2)
	}
	if os.Getenv("CAGE_PROXY_INTEGRATION") == "1" {
		if err := Validate(context.Background(), profile, s); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPartialSubscriptionImportReportsSkippedNodesAndDependencies(t *testing.T) {
	profile := t.TempDir()
	s, _ := Load(profile)
	data := `proxies:
 - {name: good, type: socks5, server: 127.0.0.1, port: 1080}
 - {name: unsupported, type: hysteria2, server: example.com, port: 443, password: secret-password, mport: secret-value}
 - {name: dependent, type: socks5, server: example.com, port: 1080, dialer-proxy: unsupported}
 - {name: unsafe, type: http, server: example.com, port: 80, ws-opts: {private-key: /private/secret-key}}
 - {name: cycle-a, type: http, server: example.com, port: 80, dialer-proxy: cycle-b}
 - {name: cycle-b, type: http, server: example.com, port: 80, dialer-proxy: cycle-a}
`
	if err := s.Import("partial", "", []byte(data), "partial", 0, ""); err != nil {
		t.Fatal(err)
	}
	if s.Count() != 1 || s.Sources[0].Nodes[0].Name != "good" {
		t.Fatal("failed nodes or broken chains survived the import")
	}
	if err := s.Save(profile); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(profile)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(loaded.Sources[0])
	var report struct {
		Skipped []struct{ Name, Reason string }
	}
	if err := json.Unmarshal(b, &report); err != nil || len(report.Skipped) != 5 {
		t.Fatalf("missing durable import report: %s, %v", b, err)
	}
	issues, _ := json.Marshal(report.Skipped)
	if !strings.Contains(string(issues), "mport") || !strings.Contains(string(issues), "dependent") {
		t.Fatal("skipped nodes have no useful diagnosis", string(issues))
	}
	for _, secret := range []string{"secret-password", "secret-value", "/private/secret-key"} {
		if strings.Contains(string(issues), secret) {
			t.Fatal("import diagnosis leaked a field value")
		}
	}
}

func TestHysteria2InvalidOrConflictingAliasesSkipOnlyThatNode(t *testing.T) {
	pin := strings.Repeat("ab", 32)
	for _, fields := range []string{
		`mport: 35000-39000, ports: 45000-49000`,
		`MPORT: 35000-39000, PORTS: 45000-49000`,
		`mport: 35000-39000, MPORT: 35000-39000`,
		`pinSHA256: "` + pin + `", fingerprint: "` + strings.Repeat("cd", 32) + `"`,
		`PINSHA256: "` + pin + `", FINGERPRINT: "` + strings.Repeat("cd", 32) + `"`,
		`pinSHA256: invalid-private-pin`,
		`pinSHA256: ""`,
		`mport: ""`,
		`mport: 0-65536`,
		`mport: {nested: secret}`,
	} {
		s, _ := Load(t.TempDir())
		data := `proxies:
 - {name: good, type: http, server: 127.0.0.1, port: 8080}
 - {name: bad, type: hysteria2, server: example.com, port: 443, password: secret, ` + fields + `}
`
		if err := s.Import("test", "", []byte(data), "test", 0, ""); err != nil || s.Count() != 1 {
			t.Fatalf("fields %s: count=%d err=%v", fields, s.Count(), err)
		}
	}
}

func TestHysteria2EquivalentAliasesAndExplicitHopInterval(t *testing.T) {
	s, _ := Load(t.TempDir())
	upper := strings.TrimSuffix(strings.Repeat("AB:", 32), ":")
	data := `payload:
 - {name: hopping, type: hysteria2, server: example.com, password: secret, mport: "443/5000-6000", ports: "443,5000-6000", hop-interval: 45, pinSHA256: "` + upper + `", fingerprint: "` + strings.Repeat("ab", 32) + `"}
 - {name: single, type: hysteria2, server: example.com, password: secret, MPORT: 35000, PINSHA256: "` + upper + `"}
`
	if err := s.Import("equivalent", "", []byte(data), "test", 0, ""); err != nil || s.Count() != 2 {
		t.Fatalf("equivalent aliases were rejected: count=%d err=%v", s.Count(), err)
	}
	first, second := s.Sources[0].Nodes[0].Config, s.Sources[0].Nodes[1].Config
	if first["ports"] != "443,5000-6000" || first["hop-interval"] != 45 || second["ports"] != "35000" || second["fingerprint"] != strings.Repeat("ab", 32) {
		t.Fatal("normalization changed port hopping semantics")
	}
}

func TestAllSkippedImportPreservesLastGoodSourceAndReportsWithoutValues(t *testing.T) {
	s, _ := Load(t.TempDir())
	if err := s.Import("test", "", []byte(testNodes), "test", 0, ""); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(s)
	data := `proxies:
 - {name: first, type: hysteria2, server: example.com, password: private-password, pinSHA256: private-pin}
 - {name: second, type: http, server: example.com, port: 80, INTERFACE_NAME: private-field}
`
	err := s.Import("test", "", []byte(data), "test", 0, "")
	if err == nil || !strings.Contains(err.Error(), "2 个") || !strings.Contains(err.Error(), "first") || !strings.Contains(err.Error(), "pinSHA256") || !strings.Contains(err.Error(), "网络接口") {
		t.Fatal("all-skipped import did not identify the failed nodes", err)
	}
	if strings.Contains(err.Error(), "private-") {
		t.Fatal("all-skipped error leaked credentials or field values")
	}
	after, _ := json.Marshal(s)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("all-skipped import replaced the last good source")
	}
}

func TestRefreshRetainsSelectedExitAndTransitWhenSkipped(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, bad := range []string{"entry", "exit"} {
			s, _ := Load(t.TempDir())
			data := `proxies:
 - {name: entry, type: http, server: 127.0.0.1, port: 8080}
 - {name: exit, type: http, server: 127.0.0.1, port: 8081, dialer-proxy: entry}
 - {name: spare, type: http, server: 127.0.0.1, port: 8082}
`
			if err := s.Import("test", "https://example.com/sub", []byte(data), "test", 24, ""); err != nil {
				t.Fatal(err)
			}
			s.Selected = s.Sources[0].Nodes[1].ID
			if explicit {
				s.Chain = []string{s.Sources[0].Nodes[0].ID, s.Selected}
			}
			before, _ := json.Marshal(s)
			broken := strings.Replace(data, "name: "+bad+",", "name: "+bad+", INTERFACE_NAME: secret,", 1)
			if err := s.Import("test", "https://example.com/sub", []byte(broken), "test", 24, ""); err == nil {
				t.Fatal("refresh accepted a missing active exit or transit")
			}
			after, _ := json.Marshal(s)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("failed refresh changed saved nodes, metadata or route")
			}
		}
	}
}
