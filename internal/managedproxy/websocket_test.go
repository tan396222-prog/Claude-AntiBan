package managedproxy

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestWebSocketAliasesSurviveImportStoreAndRuntime(t *testing.T) {
	for _, protocol := range []string{"vmess", "vless", "trojan"} {
		for _, tc := range []struct {
			name, fields, path string
			headers            map[string]any
		}{
			{"legacy", `ws-path: "/legacy?token=example", ws-headers: {host: edge.example, X-Token: example, X_Underscore: preserved}`, "/legacy?token=example", map[string]any{"Host": "edge.example", "X-Token": "example", "X_underscore": "preserved"}},
			{"variants", `WS_PATH: /variants, WS_HEADERS: {HOST: edge.example}, WS_OPTS: {max-early-data: 64}`, "/variants", map[string]any{"Host": "edge.example"}},
			{"equivalent", `ws-path: /same, ws-headers: {host: edge.example, HOST: edge.example}, ws-opts: {PATH: /same, HEADERS: {Host: edge.example}}`, "/same", map[string]any{"Host": "edge.example"}},
			{"merged-headers", `ws-path: /merged, ws-headers: {host: edge.example, X-Legacy: old}, ws-opts: {headers: {HOST: edge.example, X-Modern: new}}`, "/merged", map[string]any{"Host": "edge.example", "X-Legacy": "old", "X-Modern": "new"}},
			{"path-only", `ws-path: /path`, "/path", nil},
			{"headers-only", `ws-headers: {Host: edge.example}, ws-opts: {path: /modern}`, "/modern", map[string]any{"Host": "edge.example"}},
			{"empty-defaults", `ws-path: "", ws-headers: {}`, "", map[string]any{}},
		} {
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				profile := t.TempDir()
				store, err := Load(profile)
				if err != nil {
					t.Fatal(err)
				}
				data := fmt.Sprintf(`proxies: [{name: local, type: %s, server: 127.0.0.1, port: 443, network: ws, %s}]`, protocol, tc.fields)
				if err := store.Import("websocket", "", []byte(data), "websocket", 0, ""); err != nil {
					t.Fatal(err)
				}
				store.Selected = store.Sources[0].Nodes[0].ID
				if err := store.Save(profile); err != nil {
					t.Fatal(err)
				}
				loaded, err := Load(profile)
				if err != nil {
					t.Fatal(err)
				}
				runtime, err := loaded.Runtime(profile)
				if err != nil {
					t.Fatal(err)
				}
				var generated struct{ Proxies []map[string]any }
				if err := yaml.Unmarshal(runtime, &generated); err != nil {
					t.Fatal(err)
				}
				for _, config := range []map[string]any{loaded.Sources[0].Nodes[0].Config, generated.Proxies[0]} {
					for key := range config {
						if optionKey(key) == "ws-path" || optionKey(key) == "ws-headers" {
							t.Fatal("runtime retained an ignored WebSocket alias")
						}
					}
					opts := config["ws-opts"].(map[string]any)
					headers, _ := opts["headers"].(map[string]any)
					if opts["path"] != tc.path || !reflect.DeepEqual(headers, tc.headers) {
						t.Fatalf("WebSocket transport changed across import, store or runtime: %#v", opts)
					}
					if config["udp"] != false || config["network"] != "ws" {
						t.Fatal("conversion changed Cage UDP policy or transport")
					}
				}
				if tc.name == "variants" && generated.Proxies[0]["ws-opts"].(map[string]any)["max-early-data"] != 64 {
					t.Fatal("conversion lost unrelated WebSocket settings")
				}
			})
		}
	}
}

func TestWebSocketInvalidAliasesSkipNodeAndDependentWithoutLeakingValues(t *testing.T) {
	for _, fields := range []string{
		`ws-path: /private-path, WS_PATH: /private-path`,
		`ws-headers: {}, WS_HEADERS: {}`,
		`ws-path: /private-path, NETWORK: ws`,
		`ws-path: /private-path, ws-opts: {}, WS_OPTS: {}`,
		`ws-path: /private-path, ws-opts: {path: /private-path, PATH: /private-path}`,
		`ws-headers: {}, ws-opts: {headers: {}, HEADERS: {}}`,
		`ws-path: /private-path, ws-opts: {path: /other-private-path}`,
		`ws-path: 12`,
		`ws-path: {private-key: private-value}`,
		`ws-path: /private-path, ws-opts: private-value`,
		`ws-path: /private-path, ws-opts: {path: [private-value]}`,
		`ws-headers: private-value`,
		`ws-headers: null`,
		`ws-headers: {Host: 123}`,
		`ws-headers: {Host: [private-value]}`,
		`ws-headers: {host: private-value, HOST: different-private-value}`,
		`ws-headers: {Host: private-value}, ws-opts: {headers: {host: different-private-value}}`,
		`ws-headers: {Host: private-value}, ws-opts: {headers: private-value}`,
		`ws-headers: {"invalid header": private-value}`,
		`ws-headers: {Host: "private-value\r\nInjected: private-value"}`,
		`ws-path: /private-path, ws-opts: {PRIVATE_KEY: /private/key}`,
		`ws-path: /private-path, future-options: {CA_PATH: /private/ca}`,
		`ws-headers: {certificate: /private/certificate}`,
	} {
		t.Run(fields, func(t *testing.T) {
			store, _ := Load(t.TempDir())
			data := `proxies:
 - {name: good, type: http, server: 127.0.0.1, port: 8080}
 - {name: bad, type: vless, server: example.com, port: 443, network: ws, ` + fields + `}
 - {name: dependent, type: socks5, server: example.com, port: 1080, dialer-proxy: bad}
`
			if err := store.Import("websocket", "", []byte(data), "websocket", 0, ""); err != nil {
				t.Fatal(err)
			}
			if store.Count() != 1 || store.Sources[0].Nodes[0].Name != "good" || len(store.Sources[0].Skipped) != 2 {
				t.Fatal("invalid WebSocket node or dependent survived import")
			}
			report, _ := json.Marshal(store.Sources[0].Skipped)
			if strings.Contains(string(report), "private-") || strings.Contains(string(report), "/private/") {
				t.Fatal("import report leaked a field value")
			}
		})
	}
}

func TestWebSocketAliasesRequireAnApplicableTransport(t *testing.T) {
	for _, fields := range []string{
		`type: http, network: ws`, `type: ss, network: ws`,
		`type: vless, network: tcp`, `type: vmess, network: grpc`, `type: trojan`,
	} {
		if _, err := Parse([]byte(`proxies: [{name: example, server: example.com, port: 443, ws-headers: {Host: example.com}, `+fields+`}]`), "test"); err == nil {
			t.Fatalf("ignored fields survived incompatible transport: %s", fields)
		}
	}
	data := `proxies: [{name: example, type: http, server: example.com, port: 8080, network: grpc, ws-opts: {PATH: /retained, headers: {x_custom: example}}}]`
	nodes, err := Parse([]byte(data), "test")
	if err != nil {
		t.Fatal(err)
	}
	opts := nodes[0].Config["ws-opts"].(map[string]any)
	if opts["PATH"] != "/retained" || opts["headers"].(map[string]any)["x_custom"] != "example" {
		t.Fatal("normalization rewrote unrelated protocol data")
	}
}

func TestWebSocketRuntimeNormalizesOlderStoresWithoutMutatingThem(t *testing.T) {
	profile := t.TempDir()
	store, _ := Load(profile)
	if err := store.Import("websocket", "", []byte(`proxies: [{name: old, type: vless, server: example.com, port: 443, network: ws}]`), "test", 0, ""); err != nil {
		t.Fatal(err)
	}
	store.Selected = store.Sources[0].Nodes[0].ID
	config := store.Sources[0].Nodes[0].Config
	config["ws-path"] = "/stored"
	config["ws-headers"] = map[string]any{"host": "edge.example"}
	config["ws-opts"] = map[string]any{"PATH": "/stored", "headers": map[string]any{"X-Modern": "retained"}}
	if err := store.Save(profile); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(profile)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(loaded)
	runtime, err := loaded.Runtime(profile)
	if err != nil {
		t.Fatal(err)
	}
	var generated struct{ Proxies []map[string]any }
	if err := yaml.Unmarshal(runtime, &generated); err != nil {
		t.Fatal(err)
	}
	opts := generated.Proxies[0]["ws-opts"].(map[string]any)
	if opts["path"] != "/stored" || !reflect.DeepEqual(opts["headers"], map[string]any{"Host": "edge.example", "X-Modern": "retained"}) {
		t.Fatal("older persisted aliases were not converted")
	}
	after, _ := json.Marshal(loaded)
	if string(before) != string(after) {
		t.Fatal("runtime generation mutated stored source")
	}
	loaded.Sources[0].Nodes[0].Config["ws-path"] = "/conflict"
	if _, err := loaded.Runtime(profile); err == nil {
		t.Fatal("ambiguous older store silently selected one path")
	}
}
