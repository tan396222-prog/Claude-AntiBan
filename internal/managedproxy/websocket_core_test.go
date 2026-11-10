package managedproxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Configuration validation alone accepts legacy fields that the transport ignores.
// Observe a real local upgrade after importing, saving and reloading each protocol.
func TestCoreWebSocketAliasesReachHandshake(t *testing.T) {
	if os.Getenv("CAGE_PROXY_INTEGRATION") != "1" {
		t.Skip("requires a test binary beside the bundled Mihomo")
	}
	for _, protocol := range []string{"vmess", "vless", "trojan"} {
		for _, pathField := range []string{"WS_PATH", "ws-opts.path"} {
			t.Run(protocol+"/"+pathField, func(t *testing.T) {
				type observation struct {
					path, host, token, underscore, modern string
					upgraded                              bool
				}
				requests := make(chan observation, 8)
				upgrader := websocket.Upgrader{}
				upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
					observed := observation{
						path: request.URL.RequestURI(), host: request.Host,
						token: request.Header.Get("X-Legacy"), underscore: request.Header.Get("X_Underscore"),
						modern: request.Header.Get("X-Modern"),
					}
					connection, err := upgrader.Upgrade(w, request, nil)
					if err == nil {
						observed.upgraded = true
						_ = connection.Close()
					}
					select {
					case requests <- observed:
					default:
					}
				}))
				t.Cleanup(upstream.Close)
				_, port, err := net.SplitHostPort(upstream.Listener.Addr().String())
				if err != nil {
					t.Fatal(err)
				}
				profile := t.TempDir()
				store, err := Load(profile)
				if err != nil {
					t.Fatal(err)
				}
				pathOptions := `WS_PATH: "/legacy?source=cage", ws-opts: {headers: {HOST: edge.example, X-Modern: retained}}`
				if pathField == "ws-opts.path" {
					pathOptions = `ws-opts: {path: "/legacy?source=cage", headers: {X-Modern: retained}}`
				}
				data := fmt.Sprintf(`proxies: [{name: local, type: %s, server: 127.0.0.1, port: %s, uuid: 00000000-0000-4000-8000-000000000000, password: local-test-password, alterId: 0, cipher: auto, tls: true, skip-cert-verify: true, network: ws, WS_HEADERS: {host: edge.example, X-Legacy: local-token, X_Underscore: preserved}, %s}]`, protocol, port, pathOptions)
				if err := store.Import("local", "", []byte(data), "local", 0, ""); err != nil {
					t.Fatal(err)
				}
				store.Selected = store.Sources[0].Nodes[0].ID
				if err := store.Save(profile); err != nil {
					t.Fatal(err)
				}
				store, err = Load(profile)
				if err != nil {
					t.Fatal(err)
				}
				if err := Validate(context.Background(), profile, store); err != nil {
					t.Fatal(err)
				}
				store.Port = 0
				runtime, err := store.Runtime(profile)
				if err != nil {
					t.Fatal(err)
				}
				interfaces, err := net.Interfaces()
				if err != nil {
					t.Fatal(err)
				}
				loopback := ""
				for _, iface := range interfaces {
					if iface.Flags&net.FlagLoopback != 0 {
						loopback = iface.Name
						break
					}
				}
				runtime, err = BindInterface(runtime, loopback)
				if err != nil {
					t.Fatal(err)
				}
				config := filepath.Join(Dir(profile), "config.yaml")
				if err := Write(config, runtime); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Dir(Socket(profile)), 0700); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(Socket(profile))) })
				core, err := CorePath()
				if err != nil {
					t.Fatal(err)
				}
				log, err := os.Create(filepath.Join(profile, "core.log"))
				if err != nil {
					t.Fatal(err)
				}
				command := exec.Command(core, "-d", Dir(profile), "-f", config)
				command.Env, command.Stdout, command.Stderr = coreEnvironment(Dir(profile)), log, log
				if err := command.Start(); err != nil {
					_ = log.Close()
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait(); _ = log.Close() })
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				ticker := time.NewTicker(20 * time.Millisecond)
				defer ticker.Stop()
				for !Running(ctx, profile, store) {
					select {
					case <-ctx.Done():
						t.Fatal("generated core did not become ready")
					case <-ticker.C:
					}
				}
				// The local server closes after the WebSocket upgrade. The protocol tunnel
				// then fails, while the observed HTTP request proves transport parameters.
				_ = API(ctx, profile, "GET", "/proxies/"+store.Selected+"/delay?timeout=2000&url="+url.QueryEscape("http://127.0.0.1:1/"), nil, nil)
				select {
				case observed := <-requests:
					if observed.path != "/legacy?source=cage" || observed.host != "edge.example" || observed.token != "local-token" || observed.underscore != "preserved" || observed.modern != "retained" || !observed.upgraded {
						t.Fatalf("imported WebSocket parameters did not reach the server: %+v", observed)
					}
					t.Logf("%s WebSocket upgrade preserved path, Host and both header formats", protocol)
				case <-ctx.Done():
					t.Fatal("core never reached local WebSocket server")
				}
			})
		}
	}
}
