package workbench

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/guestos"
	"github.com/sylvinus/agent-vm/internal/managedproxy"
	"github.com/sylvinus/agent-vm/internal/remotehost"
)

// Use only a new, explicitly designated QA profile. These actions start its
// actual signed desktop app, create a real browser tab, and open a Claude Code
// terminal. No personal sign-in or prompt to a model is part of this test.
func TestWindowsFreshWorkbenchApps(t *testing.T) {
	profile, executable := os.Getenv("CAGE_TEST_WINDOWS_FRESH_PROFILE"), os.Getenv("CAGE_WINDOWS_GUEST_QA_EXECUTABLE")
	if runtime.GOOS != "windows" || profile == "" || executable == "" {
		t.Skip("requires an isolated first-run Windows guest")
	}
	binding, err := guestos.LoadWindowsProfile(profile)
	if err != nil || binding == nil || binding.NetworkPairingFile == "" {
		t.Fatal("fresh, paired Windows guest required")
	}
	expected := "Cage-" + strings.ReplaceAll(binding.VMID.String(), "-", "")[:10]
	if binding.GUIUser != expected {
		t.Fatal("use the independently provisioned standard workbench account")
	}
	s := testServer(t)
	s.profile, s.configPath, s.exe, s.windowsGuest = profile, filepath.Join(profile, "config.json"), executable, binding
	s.preferences = Preferences{Keyboard: "native"}
	s.lifetimeContext()
	defer s.lifeCancel()
	defer s.closeWindowsBrowser()
	defer s.closeWindowsNetwork()
	defer s.closeProjectShares()
	defer s.terminals.close()
	defer managedproxy.Stop(profile)
	if err := s.prepareEnvironment(); err != nil {
		t.Fatal(err)
	}
	if err := s.attachWindowsGuest(); err != nil {
		t.Fatalf("production attach: %v", err)
	}

	call := func(t *testing.T, method, path string, input, reply any) {
		t.Helper()
		var body []byte
		if input != nil {
			body, err = json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
		}
		w := windowsBrowserRequestForTest(s, method, path, body)
		if w.Code != http.StatusOK {
			t.Fatalf("%s %s: HTTP %d: %.400s", method, path, w.Code, w.Body.String())
		}
		if reply != nil && json.Unmarshal(w.Body.Bytes(), reply) != nil {
			t.Fatalf("%s returned an invalid response", path)
		}
	}
	waitJob := func(t *testing.T) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Minute)
		for s.jobs.snapshot().Running && time.Now().Before(deadline) {
			time.Sleep(250 * time.Millisecond)
		}
		job := s.jobs.snapshot()
		if job.Running || job.Error != "" {
			t.Fatalf("workbench job %s did not complete: %s", job.Kind, job.Error)
		}
	}
	online := os.Getenv("CAGE_WINDOWS_FRESH_BIND_MEASURED_EXIT") == "1"
	if online {
		// The private operator opts into pinning only this new VM's independently
		// observed managed-proxy exit. Never replace an existing anchor.
		var settings Settings
		call(t, "GET", "/api/settings", nil, &settings)
		var loc Location
		call(t, "POST", "/api/settings/probe", settings, &loc)
		if settings.ExitPolicy.NeedsBinding() {
			settings.ExitPolicy = cage.ExitPolicy{Mode: "static", IP: loc.IP}
			call(t, "POST", "/api/settings", settings, nil)
			waitJob(t)
		}
		call(t, "GET", "/api/settings", nil, &settings)
		if loc.IP == "" || settings.ExitPolicy.Mode != "static" || settings.ExitPolicy.IP != loc.IP {
			t.Fatal("selected managed exit does not match this QA account's fixed public address")
		}
		call(t, "POST", "/api/safety/check", struct{}{}, nil)
		waitJob(t)
		s.mu.Lock()
		report, gate := s.safety.Report, s.safety.Gate
		s.mu.Unlock()
		cfg, e := loadConfig(s.configPath)
		if e != nil || report == nil || gate == nil || gate.Mode != cage.GateOpen || report.Authorizes(gate.RunID, cfg.Fingerprint(), time.Now()) != nil {
			t.Fatal("actual safety report did not authorize this exit")
		}
		for _, check := range report.Checks {
			t.Logf("actual check %s: %s", check.ID, check.State)
		}
	}
	frame := func(t *testing.T, name string, f windowsWindowFrame) {
		t.Helper()
		img, e := nativeFrameImage(f)
		if e != nil {
			t.Fatalf("%s: %v", name, e)
		}
		if img.Bounds().Dx() < 240 || img.Bounds().Dy() < 180 {
			t.Fatalf("small %s frame: %v", name, img.Bounds())
		}
		if directory := os.Getenv("CAGE_WINDOWS_GUEST_QA_EVIDENCE"); directory != "" {
			dst, err := os.Create(filepath.Join(directory, name+".png"))
			if err != nil {
				t.Fatal(err)
			}
			defer dst.Close()
			if err := png.Encode(dst, img); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("owned %s frame: %dx%d", name, img.Bounds().Dx(), img.Bounds().Dy())
	}
	project := s.environmentDir()
	fixture, err := os.CreateTemp(project, "invoice-*.js")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(fixture.Name())
	if _, err = fixture.WriteString("console.log(120 * 1.1)\n"); err != nil {
		fixture.Close()
		t.Fatal(err)
	}
	if err = fixture.Close(); err != nil {
		t.Fatal(err)
	}
	call(t, "POST", "/api/projects/open", map[string]any{"workspace": project, "activate": true}, nil)
	t.Run("ClaudeDesktop", func(t *testing.T) {
		var d claudeDesktopState
		call(t, "GET", "/api/claude-desktop", nil, &d)
		if !d.Installed || !d.NativeUserSession || d.Version == "" {
			t.Fatal("signed guest desktop app not registered")
		}
		if online && d.Running && os.Getenv("CAGE_WINDOWS_FRESH_RELAUNCH_DESKTOP") == "1" {
			call(t, "POST", "/api/claude-desktop/stop", struct{}{}, nil)
			waitJob(t)
			call(t, "GET", "/api/claude-desktop", nil, &d)
		}
		if !d.Running {
			call(t, "POST", "/api/claude-desktop/start", struct{}{}, nil)
			deadline := time.Now().Add(time.Minute)
			for time.Now().Before(deadline) {
				if s.jobs.snapshot().Error != "" {
					t.Fatal(s.jobs.snapshot().Error)
				}
				time.Sleep(700 * time.Millisecond)
				call(t, "GET", "/api/claude-desktop", nil, &d)
				if d.Running {
					break
				}
			}
		}
		if !d.Running {
			t.Fatal("official Claude window did not open")
		}
		// A newly launched Electron window can be uniformly white during initial
		// navigation. The guest refuses these frames until it has real content.
		var f windowsWindowFrame
		ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
		defer cancel()
		for {
			if err := s.windowsDesktopRequest(ctx, "frame", nil, &f); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("official Windows window did not render readable content")
			case <-time.After(500 * time.Millisecond):
			}
		}
		frame(t, "windows-fresh-claude", f)
		if script := os.Getenv("CAGE_WINDOWS_QA_UI_SCRIPT"); online && script != "" {
			// Issue ephemeral authenticated cookies only to our local private smoke
			// runner over stdin. This exercises the actual renderer and WebSocket.
			s.token, s.session = randomToken(), randomToken()
			server := httptest.NewServer(s.handler())
			defer server.Close()
			s.origin = strings.Replace(server.URL, "127.0.0.1", "localhost", 1)
			authCookies := func(target *Server) []*http.Cookie {
				return []*http.Cookie{
					{Name: target.cookieName(), Value: target.session, Path: "/", HttpOnly: true},
					{Name: target.cookieName() + "-desktop", Value: trustedViewProof(target.token), Path: "/", HttpOnly: true},
				}
			}
			jar, _ := cookiejar.New(nil)
			upstream, _ := url.Parse(s.origin)
			jar.SetCookies(upstream, authCookies(s))
			session, e := remotehost.LocalSession(s.lifetimeContext(), s.origin, remotehost.PublicManifest{Platform: "windows", InstanceID: randomToken()}, jar)
			if e != nil {
				t.Fatal(e)
			}
			defer session.Close()
			gateway := testServer(t)
			gateway.token, gateway.session = randomToken(), randomToken()
			gateway.desktopEntry = true
			gateway.remote = newDesktopGateway(gateway.profile, &desktopLocal{profile: s.profile})
			d := gateway.remote.desktop
			d.active = &desktopTarget{id: d.generation, mode: "local", ctx: s.lifetimeContext(), session: session}
			gatewayServer := httptest.NewServer(gateway.handler())
			defer gatewayServer.Close()
			gateway.origin = gatewayServer.URL
			payload, _ := json.Marshal(map[string]any{
				"url": gateway.origin, "evidence": os.Getenv("CAGE_WINDOWS_GUEST_QA_EVIDENCE"),
				"cookies": []map[string]string{
					{"name": gateway.cookieName(), "value": gateway.session}, {"name": gateway.cookieName() + "-desktop", "value": trustedViewProof(gateway.token)},
				},
			})
			uiCtx, stop := context.WithTimeout(context.Background(), 2*time.Minute)
			defer stop()
			cmd := exec.CommandContext(uiCtx, os.Getenv("CAGE_WINDOWS_QA_NODE"), script)
			cmd.Stdin = bytes.NewReader(payload)
			if out, e := cmd.CombinedOutput(); e != nil {
				t.Fatalf("shared workbench click: %v (%.600s)", e, out)
			}
			t.Log("tested actual shared React/noVNC window interaction")
			call(t, "POST", "/api/claude-desktop/resize", map[string]int{"width": 1100, "height": 760}, nil)
			resizeCtx, release := context.WithTimeout(context.Background(), 12*time.Second)
			defer release()
			var resized windowsWindowFrame
			if e := s.windowsDesktopRequest(resizeCtx, "frame", nil, &resized); e != nil {
				t.Fatal(e)
			}
			frame(t, "windows-fresh-resized-claude", resized)
			if resized.Width != 1100 || resized.Height != 760 {
				t.Errorf("Windows returned %dx%d for a 1100x760 panel", resized.Width, resized.Height)
			}
		}
	})
	t.Run("BrowserAndEditor", func(t *testing.T) {
		request := func(action string, payload map[string]any) string {
			t.Helper()
			var tab struct {
				ID string `json:"id"`
			}
			call(t, "POST", "/api/browser/"+action, payload, &tab)
			if tab.ID == "" {
				t.Fatalf("%s did not create a tab", action)
			}
			return tab.ID
		}
		startURL := "about:blank"
		if online {
			startURL = "https://example.com/"
		}
		tab := request("new", map[string]any{"url": startURL})
		defer call(t, "POST", "/api/browser/close", map[string]any{"id": tab}, nil)
		read := func(name string) {
			t.Helper()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			b, e := s.nativeBrowserClient().request(ctx, "GET", "/frame", nil)
			if e != nil {
				t.Fatal(e)
			}
			var f windowsWindowFrame
			if json.Unmarshal(b, &f) != nil {
				t.Fatal("invalid guest browser frame")
			}
			frame(t, name, f)
		}
		if online {
			time.Sleep(3 * time.Second)
		}
		read("windows-fresh-browser")
		editor := request("editor", map[string]any{"workspace": project})
		defer call(t, "POST", "/api/browser/close", map[string]any{"id": editor}, nil)
		time.Sleep(3 * time.Second)
		read("windows-fresh-editor")
	})
	t.Run("PowerShell7", func(t *testing.T) {
		var session struct {
			ID string `json:"id"`
		}
		call(t, "POST", "/api/terminals", map[string]any{"kind": "shell", "workspace": project}, &session)
		terminal := s.terminals.get(session.ID)
		if terminal == nil {
			t.Fatal("workbench shell was not created")
		}
		defer s.terminals.remove(session.ID)
		const marker = "CageInstalledShellVerified"
		encoded := base64.StdEncoding.EncodeToString([]byte(marker))
		line := "$v = (& pwsh --version).Trim(); if ($v -match '^PowerShell 7[.]') { [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('" + encoded + "')) + $v }"
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if e := terminal.enqueueInput(ctx, terminalInputLine(line)); e != nil {
			t.Fatal(e)
		}
		version := regexp.MustCompile(marker + `PowerShell 7[.][0-9]+[.][0-9]+`)
		for ctx.Err() == nil {
			terminal.mu.Lock()
			m := version.Find(terminal.history)
			terminal.mu.Unlock()
			if m != nil {
				t.Log("standard user's pwsh --version:", string(m[len(marker):]))
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
		t.Fatal("PowerShell 7 was not directly executable in the normal workbench terminal")
	})
	t.Run("ClaudeCode", func(t *testing.T) {
		var session struct {
			ID           string `json:"id"`
			Conversation string `json:"cli_session_id"`
		}
		call(t, "POST", "/api/terminals", map[string]any{"kind": "claude", "workspace": project}, &session)
		terminal := s.terminals.get(session.ID)
		if terminal == nil || session.Conversation == "" {
			t.Fatal("shared Claude Code terminal not created")
		}
		defer s.terminals.remove(session.ID)
		deadline := time.Now().Add(time.Minute)
		for time.Now().Before(deadline) {
			terminal.mu.Lock()
			text := strings.ToLower(string(terminal.history))
			closed := terminal.closed
			terminal.mu.Unlock()
			plain := regexp.MustCompile("\\x1b\\[[0-9;>?]*[a-zA-Z]").ReplaceAllString(text, "")
			plain = strings.Join(strings.Fields(plain), "")
			if strings.Contains(plain, "welcometoclaudecode") || strings.Contains(text, "claude code") || strings.Contains(text, "login method") || strings.Contains(text, "choose the text style") {
				t.Log("shared terminal delivered official Claude Code onboarding")
				return
			}
			if closed {
				break
			}
			time.Sleep(400 * time.Millisecond)
		}
		t.Fatal("workbench did not receive official Claude Code onboarding")

	})
}
