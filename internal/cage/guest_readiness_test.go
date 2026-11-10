package cage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestGuestReadinessWaitsForAuthenticatedBrowser(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python unavailable")
	}
	var browserRequests, proxyRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/browser" {
			_, _ = fmt.Fprint(w, "ready")
			return
		}
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			w.WriteHeader(401)
			return
		}
		if browserRequests.Add(1) < 3 {
			w.WriteHeader(503)
			return
		}
		_, _ = fmt.Fprint(w, `{"ready":true}`)
	}))
	defer server.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { proxyRequests.Add(1); w.WriteHeader(502) }))
	defer proxy.Close()
	dir := t.TempDir()
	script, config := filepath.Join(dir, "wait.py"), filepath.Join(dir, "browser.json")
	if err := os.WriteFile(script, []byte(guestWaitServices), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(`{"token":"fixture-token"}`), 0600); err != nil {
		t.Fatal(err)
	}
	endpoints, _ := json.Marshal([][]any{{server.URL + "/editor", false}, {server.URL + "/vnc", false}, {server.URL + "/browser", true}})
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-c", `import runpy,sys,json;runpy.run_path(sys.argv[1])['wait'](sys.argv[2],json.loads(sys.argv[3]),timeout=10)`, script, config, string(endpoints))
	cmd.Env = append(os.Environ(), "http_proxy="+proxy.URL, "HTTP_PROXY="+proxy.URL, "no_proxy=", "NO_PROXY=")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("readiness: %v %s", err, output)
	}
	if browserRequests.Load() < 3 || proxyRequests.Load() != 0 {
		t.Fatalf("premature or proxied readiness: browser=%d proxy=%d", browserRequests.Load(), proxyRequests.Load())
	}
}
