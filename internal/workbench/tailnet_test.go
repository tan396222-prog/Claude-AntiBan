package workbench

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTailnetAuthorizationURL(t *testing.T) {
	for _, u := range []string{"https://login.tailscale.com/a/abc", "https://login.tailscale.com/c/abc"} {
		if !validTailnetAuthURL(u) {
			t.Fatal(u)
		}
	}
	for _, u := range []string{"http://login.tailscale.com/a/abc", "https://login.tailscale.com.evil.test/a/abc", "https://evil.test/a/abc", "https://user@login.tailscale.com/a/abc", "https://login.tailscale.com:444/a/abc", "javascript:alert(1)", "https://login.tailscale.com/admin"} {
		if validTailnetAuthURL(u) {
			t.Fatal(u)
		}
	}
}
func TestTailnetBoundaries(t *testing.T) {
	s := testServer(t)
	if w := request(s, "POST", "/api/tailscale/connect", "https://evil.test", "{}", true); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := request(s, "GET", "/api/tailscale", "", "", false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := request(s, "GET", "/api/tailscale", "", "", true); w.Code != 409 {
		t.Fatal(w.Code)
	}
	for _, path := range []string{"/api/tailscale", "/api/tailscale/login", "/api/tailscale/add-key"} {
		if !macOSWorkbenchRoute(path) || !windowsWorkbenchRoute(path) {
			t.Fatal(path)
		}
	}
	if !validWindowsBrowserRoute("POST", "/tailnet") || validWindowsBrowserRoute("GET", "/tailnet") {
		t.Fatal("native guest route method changed")
	}
}

func TestTailnetGuestContract(t *testing.T) {
	s := testServer(t)
	s.ready = true
	s.credentials.Token = "test-tailnet-token"
	guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/tailnet" || r.Header.Get("Authorization") != "Bearer test-tailnet-token" {
			t.Error("wrong guest route or credentials")
		}
		var input map[string]string
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			t.Error("invalid input")
		}
		if input["action"] == "add-key" {
			_ = json.NewEncoder(w).Encode(map[string]string{"tailnet_error": "请粘贴一条有效的 SSH 公钥"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"installed": true, "backend": "NeedsLogin", "auth_url": "https://login.tailscale.com/a/test"})
	}))
	defer guest.Close()
	s.browserControl = localBrowserClient(t, s.lifetimeContext(), guest)
	if w := request(s, "POST", "/api/tailscale/connect", s.origin, "{}", true); w.Code != 200 || !strings.Contains(w.Body.String(), "login.tailscale.com") {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
	if w := request(s, "POST", "/api/tailscale/add-key", s.origin, `{"key":"invalid"}`, true); w.Code != 502 || !strings.Contains(w.Body.String(), "有效的 SSH 公钥") {
		t.Fatalf("key error: %d %s", w.Code, w.Body.String())
	}
}

func TestTailnetServicePortContract(t *testing.T) {
	s := testServer(t)
	s.ready = true
	s.credentials.Token = "test-tailnet-token"
	guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Action string `json:"action"`
			Host   string `json:"host"`
			Port   int    `json:"port"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if input.Action != "open-service" || input.Host != "100.88.2.4" || input.Port != 8080 {
			t.Errorf("wrong service input: %+v", input)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"installed": true, "backend": "Running", "forwards": []map[string]any{{"host": input.Host, "port": input.Port, "address": "127.0.0.1:32123"}}})
	}))
	defer guest.Close()
	s.browserControl = localBrowserClient(t, s.lifetimeContext(), guest)
	if w := request(s, "POST", "/api/tailscale/open-service", s.origin, `{"host":"100.88.2.4","port":8080}`, true); w.Code != 200 || !strings.Contains(w.Body.String(), "127.0.0.1:32123") {
		t.Fatalf("service: %d %s", w.Code, w.Body.String())
	}
	for _, body := range []string{`{"host":"100.88.2.4","port":0}`, `{"host":"100.88.2.4","port":65536}`, `{"host":"100.88.2.4","port":"80"}`, `{"port":80}`} {
		if w := request(s, "POST", "/api/tailscale/open-service", s.origin, body, true); w.Code != 400 {
			t.Fatalf("bad input accepted: %d %s", w.Code, body)
		}
	}
	if !macOSWorkbenchRoute("/api/tailscale/open-service") || !windowsWorkbenchRoute("/api/tailscale/open-service") {
		t.Fatal("native hosts must expose the same service route")
	}
}
