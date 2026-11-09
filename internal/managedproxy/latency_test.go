package managedproxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

// This runs the bundled core against local servers. A slow CONNECT handshake
// must affect elapsed time without inflating the displayed request latency.
func TestCoreLatencyExcludesProxyHandshake(t *testing.T) {
	if os.Getenv("CAGE_PROXY_INTEGRATION") != "1" {
		t.Skip("requires a test binary beside the bundled Mihomo")
	}
	var requests, connections atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Error("latency probe must use HEAD")
		}
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)
	const handshake = 500 * time.Millisecond
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			t.Error("expected proxy CONNECT")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		remote, err := net.Dial("tcp", strings.TrimPrefix(target.URL, "http://"))
		if err != nil {
			t.Error(err)
			return
		}
		defer remote.Close()
		client, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer client.Close()
		connections.Add(1)
		time.Sleep(handshake)
		if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		go func() { _, _ = io.Copy(remote, buffered) }()
		_, _ = io.Copy(client, remote)
	}))
	t.Cleanup(upstream.Close)

	profile := t.TempDir()
	s, err := Load(profile)
	if err != nil {
		t.Fatal(err)
	}
	address := strings.TrimPrefix(upstream.URL, "http://")
	_, port, _ := net.SplitHostPort(address)
	if err := s.Import("local", "", []byte("proxies: [{name: local, type: http, server: 127.0.0.1, port: "+port+"}]"), "local", 0, ""); err != nil {
		t.Fatal(err)
	}
	s.Selected, s.Port = s.Sources[0].Nodes[0].ID, 0
	data, err := s.Runtime(profile)
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
	data, err = BindInterface(data, loopback)
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(Dir(profile), "config.yaml")
	if err := Write(config, data); err != nil {
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
	cmd := exec.Command(core, "-d", Dir(profile), "-f", config)
	cmd.Env, cmd.Stdout, cmd.Stderr = coreEnvironment(Dir(profile)), log, log
	if err := cmd.Start(); err != nil {
		log.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait(); _ = log.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for !Running(ctx, profile, s) {
		select {
		case <-ctx.Done():
			t.Fatal("generated configuration did not become ready")
		case <-ticker.C:
		}
	}
	var result struct {
		Delay int `json:"delay"`
	}
	start := time.Now()
	if err := API(ctx, profile, "GET", "/proxies/"+s.Selected+"/delay?timeout=5000&url="+url.QueryEscape(target.URL), nil, &result); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if result.Delay <= 0 || elapsed-time.Duration(result.Delay)*time.Millisecond < handshake-100*time.Millisecond || requests.Load() != 2 || connections.Load() != 1 {
		t.Fatalf("delay=%dms elapsed=%s HEADs=%d connections=%d; expected two requests on one connection with handshake excluded", result.Delay, elapsed, requests.Load(), connections.Load())
	}
	t.Logf("reported=%dms elapsed=%s HEADs=%d CONNECTs=%d", result.Delay, elapsed, requests.Load(), connections.Load())
	var legacy map[string]any
	if err := yaml.Unmarshal(data, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy["unified-delay"] = false
	legacyData, err := yaml.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := API(ctx, profile, "PUT", "/configs", map[string]string{"payload": string(legacyData)}, nil); err != nil {
		t.Fatal(err)
	}
	if Running(ctx, profile, s) {
		t.Fatal("core using the old latency metric was accepted for reuse")
	}
}
