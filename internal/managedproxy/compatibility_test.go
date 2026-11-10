package managedproxy

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestProtocolOptionsSurviveImportStoreAndRuntime(t *testing.T) {
	for _, tc := range []struct{ name, fields string }{
		{"xudp", "xudp: true"},
		{"xudp-false", "xudp: false"},
		{"omitted", ""},
		{"packet-encoding", "packet-encoding: xudp"},
		{"both", "xudp: true, packet-encoding: packetaddr"},
		{"packet-addr", "packet-addr: true"},
		{"core-key-variant", "XUDP: true, packet_encoding: xudp"},
		{"extra-options", "future-option: {enabled: true}, xhttp-opts: {path: /tunnel, headers: {X-Custom-Header: value}}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile := t.TempDir()
			s, err := Load(profile)
			if err != nil {
				t.Fatal(err)
			}
			// X25519's base point is a valid public key; no real node is used.
			data := `proxies: [{name: example, type: vless, server: example.com, port: 443, uuid: 00000000-0000-4000-8000-000000000000, tls: true, servername: example.com, udp: true, reality-opts: {public-key: CQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA, short-id: "0123456789abcdef", support-x25519mlkem768: true}, ` + tc.fields + `}]`
			if err := s.Import("feedback", "", []byte(data), "feedback", 0, ""); err != nil {
				t.Fatal(err)
			}
			s.Selected = s.Sources[0].Nodes[0].ID
			if err := s.Save(profile); err != nil {
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
			var original, generated struct{ Proxies []map[string]any }
			if err := yaml.Unmarshal([]byte(data), &original); err != nil {
				t.Fatal(err)
			}
			if err := yaml.Unmarshal(runtime, &generated); err != nil || len(generated.Proxies) != 1 {
				t.Fatal("runtime lost the imported node", err)
			}
			want := original.Proxies[0]
			want["name"], want["udp"] = loaded.Selected, false
			if !reflect.DeepEqual(generated.Proxies[0], want) {
				t.Fatal("protocol fields changed during import, persistence or runtime generation")
			}
			if os.Getenv("CAGE_PROXY_INTEGRATION") == "1" {
				if err := Validate(context.Background(), profile, loaded); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestImportBoundaryRecognizesCoreKeyVariants(t *testing.T) {
	for _, fields := range []string{
		"interface_name: private-interface",
		"INTERFACE-NAME: private-interface",
		"Routing_Mark: 42",
		"CA: /private/ca.pem",
		"ca_path: /private/ca.pem",
		"Certificate: /private/client.pem",
		"PRIVATE_KEY: /private/key.pem",
		"xhttp-opts: {download-settings: {PRIVATE_KEY: /private/key.pem}}",
		"plugin-opts: {Certificate: /private/client.pem}",
		"future-option: [{TLS_OPTS: {CA: /private/ca.pem}}]",
		"xhttp-opts: {123: value, download-settings: {PRIVATE_KEY: /private/key.pem}}",
		"DIALER_PROXY: DIRECT",
		"dialer-proxy: DIRECT, DIALER_PROXY: private-target",
		"udp: false, UDP: true",
		"NAME: private-other-name",
	} {
		t.Run(fields, func(t *testing.T) {
			data := "proxies: [{name: example, type: http, server: example.com, port: 443, " + fields + "}]"
			if _, err := Parse([]byte(data), "test"); err == nil || strings.Contains(err.Error(), "private-") {
				t.Fatal("unsafe or ambiguous fields were accepted, or values leaked", err)
			}
		})
	}
	data := `proxies:
 - {NAME: entry, TYPE: http, SERVER: example.com, port: 8080, UDP: true}
 - {name: exit, type: http, server: example.com, port: 8081, DIALER_PROXY: entry, ws-opts: {PATH: /tunnel}}
`
	nodes, err := Parse([]byte(data), "test")
	if err != nil || len(nodes) != 2 {
		t.Fatal("valid key variants rejected", err)
	}
	if nodes[0].Config["udp"] != false || nodes[0].Config["UDP"] != nil || nodes[1].Config["dialer-proxy"] != nodes[0].ID || nodes[1].Config["DIALER_PROXY"] != nil {
		t.Fatal("key variants escaped Cage's UDP policy or chain namespacing")
	}
}

func TestCoreRejectsInvalidProtocolParameterAfterImport(t *testing.T) {
	if os.Getenv("CAGE_PROXY_INTEGRATION") != "1" {
		t.Skip("requires a test binary beside the bundled Mihomo")
	}
	profile := t.TempDir()
	s, _ := Load(profile)
	data := `proxies: [{name: example, type: vless, server: example.com, port: 443, uuid: 00000000-0000-4000-8000-000000000000, xudp: {invalid: private-value}}]`
	if err := s.Import("feedback", "", []byte(data), "feedback", 0, ""); err != nil {
		t.Fatal("protocol validation should be left to the core", err)
	}
	s.Selected = s.Sources[0].Nodes[0].ID
	err := Validate(context.Background(), profile, s)
	if err == nil || !strings.Contains(err.Error(), "Mihomo") || strings.Contains(err.Error(), "private-value") {
		t.Fatal("core validation did not reject the invalid parameter safely", err)
	}
}
