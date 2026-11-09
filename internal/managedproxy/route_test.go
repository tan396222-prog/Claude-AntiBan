package managedproxy

import (
	"reflect"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestRouteOverridesAcrossSourcesWithoutRewritingSubscription(t *testing.T) {
	profile := t.TempDir()
	s, _ := Load(profile)
	if err := s.Import("airport", "", []byte(testNodes), "airport", 0, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Import("home", "", []byte(`proxies: [{name: home, type: socks5, server: 127.0.0.1, port: 1081, password: private-home}]`), "home", 0, ""); err != nil {
		t.Fatal(err)
	}
	entry := s.Sources[0].Nodes[1] // Already has an imported dialer, which the explicit entry must override.
	exit := s.Sources[1].Nodes[0]
	s.Selected = exit.ID
	s.Chain = []string{entry.ID, exit.ID}
	data, err := s.Runtime(profile)
	if err != nil {
		t.Fatal(err)
	}
	var runtime struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(data, &runtime); err != nil {
		t.Fatal(err)
	}
	if runtime.Proxies[1]["dialer-proxy"] != nil || runtime.Proxies[2]["dialer-proxy"] != entry.ID {
		t.Fatal("wrong entry/exit order", runtime.Proxies)
	}
	if s.Sources[0].Nodes[1].Config["dialer-proxy"] == nil || s.Sources[1].Nodes[0].Config["dialer-proxy"] != nil {
		t.Fatal("runtime changed subscription nodes")
	}
	if err := s.Save(profile); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(profile)
	if err != nil {
		t.Fatal(err)
	}
	if route, err := loaded.Route(); err != nil || !reflect.DeepEqual(route, s.Chain) {
		t.Fatal("chain was not durable", route, err)
	}
	if err := loaded.Import("home", "", []byte(`proxies: [{name: home, type: socks5, server: 127.0.0.1, port: 2081, password: refreshed-home}]`), "home", 0, ""); err != nil {
		t.Fatal(err)
	}
	if route, err := loaded.Route(); err != nil || !reflect.DeepEqual(route, s.Chain) {
		t.Fatal("refresh lost the explicit chain", route, err)
	}
	loaded.Chain = []string{entry.ID}
	loaded.Selected = entry.ID
	data, err = loaded.Runtime(profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, &runtime); err != nil {
		t.Fatal(err)
	}
	if runtime.Proxies[1]["dialer-proxy"] != nil {
		t.Fatal("single-node mode retained imported transit")
	}
}

func TestRouteReadsLegacyImportedChainsAndRejectsBrokenExplicitChains(t *testing.T) {
	s, _ := Load(t.TempDir())
	if err := s.Import("legacy", "", []byte(testNodes), "legacy", 0, ""); err != nil {
		t.Fatal(err)
	}
	entry, exit := s.Sources[0].Nodes[0].ID, s.Sources[0].Nodes[1].ID
	s.Selected = exit
	if route, err := s.Route(); err != nil || !reflect.DeepEqual(route, []string{entry, exit}) {
		t.Fatal("legacy chain changed", route, err)
	}
	for _, chain := range [][]string{{entry, entry, exit}, {"missing", exit}, {entry}} {
		s.Chain = chain
		if _, err := s.Runtime(t.TempDir()); err == nil {
			t.Fatal("invalid chain accepted", chain)
		}
	}
}
