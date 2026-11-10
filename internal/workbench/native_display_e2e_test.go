package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sylvinus/agent-vm/internal/diagnostics"
	"github.com/sylvinus/agent-vm/internal/guestos"
	"github.com/sylvinus/agent-vm/internal/remotehost"
)

// Opt-in against an isolated, unlocked guest. The real workbench handlers,
// native guest relay and React/noVNC renderer all participate in this check.
func TestWindowsNativeDisplayWorkbench(t *testing.T) {
	profile := os.Getenv("CAGE_NATIVE_DISPLAY_PROFILE")
	script := os.Getenv("CAGE_NATIVE_DISPLAY_SCRIPT")
	if runtime.GOOS != "windows" || profile == "" || script == "" {
		t.Skip("requires an isolated Windows guest and the native display runner")
	}
	evidence := os.Getenv("CAGE_NATIVE_DISPLAY_EVIDENCE")
	if evidence == "" {
		evidence = t.TempDir()
	}
	if err := os.MkdirAll(evidence, 0700); err != nil {
		t.Fatal(err)
	}
	binding, err := guestos.LoadWindowsProfile(profile)
	if err != nil || binding == nil {
		t.Fatalf("native display profile: %v", err)
	}
	s := testServer(t)
	previousDiagnostics := diagnostics.Default()
	diagnostics.SetDefault(diagnostics.New(s.profile))
	defer diagnostics.SetDefault(previousDiagnostics)
	defer func() {
		if data, err := os.ReadFile(filepath.Join(s.profile, "logs", "events.jsonl")); err == nil {
			if err := os.WriteFile(filepath.Join(evidence, "native-browser-events.jsonl"), data, 0600); err != nil {
				t.Errorf("write native display diagnostics: %v", err)
			}
			if t.Failed() {
				t.Log(string(data))
			}
		}
	}()
	if err := guestos.BindWindowsProfile(s.profile, *binding); err != nil {
		t.Fatal(err)
	}
	s.windowsGuest, s.ready = binding, true
	s.windowsState.Running = true
	s.preferences.Keyboard = "native"
	s.preferences.BrowserHomeURL = os.Getenv("CAGE_NATIVE_DISPLAY_FIXTURE")
	s.lifetimeContext()
	defer s.lifeCancel()
	defer s.closeWindowsBrowser()
	data, err := s.nativeBrowserClient().request(s.lifetimeContext(), "GET", "/frame", nil)
	if err != nil {
		t.Fatal(err)
	}
	var initial windowsWindowFrame
	if err := json.Unmarshal(data, &initial); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := nativeTightRectangles(initial, -260); err != nil {
		t.Fatalf("native encoded frame: %v", err)
	}
	t.Logf("native frame validated at %dx%d", initial.Width, initial.Height)
	s.token, s.session = randomToken(), randomToken()
	server := httptest.NewServer(s.handler())
	defer server.Close()
	s.origin = strings.Replace(server.URL, "127.0.0.1", "localhost", 1)
	jar, _ := cookiejar.New(nil)
	upstream, _ := url.Parse(s.origin)
	jar.SetCookies(upstream, []*http.Cookie{
		{Name: s.cookieName(), Value: s.session, Path: "/", HttpOnly: true},
		{Name: s.cookieName() + "-desktop", Value: trustedViewProof(s.token), Path: "/", HttpOnly: true},
	})
	session, err := remotehost.LocalSession(s.lifetimeContext(), s.origin, remotehost.PublicManifest{Platform: "windows", InstanceID: randomToken()}, jar)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	gateway := testServer(t)
	gateway.token, gateway.session = randomToken(), randomToken()
	gateway.desktopEntry = true
	gateway.remote = newDesktopGateway(gateway.profile, &desktopLocal{profile: s.profile})
	desktop := gateway.remote.desktop
	desktop.active = &desktopTarget{id: desktop.generation, mode: "local", ctx: s.lifetimeContext(), session: session}
	entry := httptest.NewServer(gateway.handler())
	defer entry.Close()
	gateway.origin = entry.URL
	for _, target := range []*Server{s, gateway} {
		headers := http.Header{"Origin": {target.origin}, "Cookie": {target.cookieName() + "=" + target.session + "; " + target.cookieName() + "-desktop=" + trustedViewProof(target.token)}}
		connection := "local"
		if target == gateway {
			connection = desktop.generation
		}
		ws, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(target.origin, "http")+"/api/vnc?connection="+connection, headers)
		if err != nil {
			detail := ""
			if response != nil {
				body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
				response.Body.Close()
				detail = string(body)
			}
			t.Fatalf("native WebSocket upgrade: %v %s", err, detail)
		}
		ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, greeting, err := ws.ReadMessage()
		ws.Close()
		if err != nil || string(greeting) != "RFB 003.008\n" {
			t.Fatalf("native WebSocket greeting, gateway=%v: %v", target == gateway, err)
		}
	}
	payload, err := json.Marshal(map[string]any{
		"url": gateway.origin, "fixture": s.preferences.BrowserHomeURL,
		"evidence": evidence,
		"cookies": []map[string]string{
			{"name": gateway.cookieName(), "value": gateway.session},
			{"name": gateway.cookieName() + "-desktop", "value": trustedViewProof(gateway.token)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	node := os.Getenv("CAGE_NATIVE_DISPLAY_NODE")
	if node == "" {
		node = "node"
	}
	cmd := exec.CommandContext(ctx, node, script)
	cmd.Stdin = bytes.NewReader(payload)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native display workbench: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}
