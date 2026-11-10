//go:build e2e && darwin && arm64

package workbench

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/gorilla/websocket"
	"github.com/sylvinus/agent-vm/internal/guestos"
)

//go:embed testdata/macos_editor_file.swift
var macOSEditorFileLocator []byte

// Exercises the same local-view HTTP admission, SSH PTY and WebSocket stream
// as the installed Mac workbench. Requires an already started, bound guest.
func TestMacOSWorkbenchTerminal(t *testing.T) {
	profile := os.Getenv("CAGE_TEST_MACOS_WORKBENCH_PROFILE")
	if profile == "" {
		t.Skip("select a prepared test profile with CAGE_TEST_MACOS_WORKBENCH_PROFILE")
	}
	binding, err := guestos.LoadMacOSProfile(profile)
	if err != nil || binding == nil {
		t.Fatalf("unavailable macOS test profile: %v", err)
	}
	s := testServer(t)
	s.profile, s.macosGuest = profile, binding
	s.token, s.session = randomToken(), randomToken()
	s.preferences.Workspace = s.environmentDir()
	ctx, cancel := s.controlContext()
	defer cancel()
	if err := s.checkMacOSGuest(ctx); err != nil {
		t.Fatal(err)
	}
	s.ready = true
	defer s.terminals.close()
	server := httptest.NewServer(s.handler())
	defer server.Close()
	s.origin = server.URL
	cookie := s.cookieName() + "=" + s.session + "; " + s.cookieName() + "-desktop=" + trustedViewProof(s.token)
	post := func(route string, body map[string]any) (int, string) {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r, err := http.NewRequest("POST", server.URL+"/api/"+route, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", server.URL)
		r.Header.Set("Cookie", cookie)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, string(out)
	}
	defer s.closeProjectShares()
	project := os.Getenv("CAGE_TEST_MACOS_WORKBENCH_PROJECT")
	var fixture string
	if project != "" {
		fixture, err = os.MkdirTemp(project, ".cage-live-workbench-")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(fixture)
		if err = os.Mkdir(filepath.Join(fixture, ".git"), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(fixture, "from-host"), []byte("host-data"), 0600); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(4 * time.Minute)
		for {
			code, message := post("projects/open", map[string]any{"workspace": project, "add": true})
			if code == 200 && !strings.Contains(message, `"approval":`) {
				break
			}
			if code == 200 && strings.Contains(message, `"approval":`) && time.Now().Before(deadline) {
				time.Sleep(2 * time.Second)
				continue
			}
			if strings.TrimSpace(message) != errMacOSProjectApproval.Error() || time.Now().After(deadline) {
				if strings.Contains(message, `"approval":`) {
					t.Fatal("waiting for guest project approval timed out")
				}
				t.Fatalf("mount: %d %s", code, message)
			}
			time.Sleep(2 * time.Second)
		}
	}
	code, reply := post("terminals", map[string]any{"kind": "shell", "workspace": project})
	if code != 200 {
		t.Fatalf("terminal: %d %s", code, reply)
	}
	var tab struct{ ID string }
	if err := json.Unmarshal([]byte(reply), &tab); err != nil || tab.ID == "" {
		t.Fatalf("invalid terminal response: %v", err)
	}
	h := http.Header{"Origin": {s.origin}, "Cookie": {cookie}}
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/terminals/"+tab.ID+"/stream", h)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if err := ws.WriteJSON(map[string]any{"type": "resize", "cols": 92, "rows": 31}); err != nil {
		t.Fatal(err)
	}
	// The literal expected string is absent from the submitted command; an
	// echoed keystroke cannot satisfy the guest identity or compilation check.
	cmd := `d=$(mktemp -d) && printf '%s\n' 'return 6 * 7' > "$d/source.applescript" && /usr/bin/osacompile -o "$d/test.scpt" "$d/source.applescript" && printf 'CAGE_TRIAL_%s_%s_%s_END\n' "$(uname -s)" "$(sw_vers -productVersion)" "$(osascript "$d/test.scpt")"; rm -f "$d/source.applescript" "$d/test.scpt"; rmdir "$d"`
	expected := "_42_END"
	if project != "" {
		base := filepath.Base(fixture)
		cmd = fmt.Sprintf(`d=%s; test "$(cat "$d/from-host")" = host-data && ! touch "$d/.git/forbidden" && printf guest-data > "$d/from-guest.tmp" && mv "$d/from-guest.tmp" "$d/from-guest" && printf 'CAGE_PROJECT_%%s_%%s_%%s_END\n' "$(uname -s)" "$(./run.sh)" "$(stty size)"`, base)
		expected = "CAGE_PROJECT_Darwin_132_31 92_END"
	}
	if err := ws.WriteJSON(map[string]string{"type": "input", "data": cmd + "\r"}); err != nil {
		t.Fatal(err)
	}
	_ = ws.SetReadDeadline(time.Now().Add(35 * time.Second))
	var output strings.Builder
	for !strings.Contains(output.String(), expected) {
		_, chunk, err := ws.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		output.Write(chunk)
	}
	if project != "" {
		data, err := os.ReadFile(filepath.Join(fixture, "from-guest"))
		if err != nil || string(data) != "guest-data" {
			t.Fatal("lost atomic guest save")
		}
		if _, err = os.Stat(filepath.Join(fixture, ".git", "forbidden")); !os.IsNotExist(err) {
			t.Fatal("guest modified protected Git metadata")
		}
		if os.Getenv("CAGE_TEST_MACOS_WORKBENCH_BOUNDARY") == "1" {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			probe, err := os.CreateTemp(project, ".cage-share-probe-*")
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(probe.Name())
			challenge := randomToken()
			_, err = probe.WriteString(challenge)
			probe.Close()
			if err != nil {
				t.Fatal(err)
			}
			private, err := os.CreateTemp(s.profile, ".cage-private-probe-*")
			if err != nil {
				t.Fatal(err)
			}
			private.Close()
			defer os.Remove(private.Name())
			client, err := s.gateClient()
			if err != nil {
				t.Fatal(err)
			}
			before, err := client.Status(ctx)
			if err != nil || before.Mode != "closed" {
				t.Fatal("require an offline test guest")
			}
			boundary, err := s.guestObservation(ctx, boundaryProbeInput(17777, randomToken()[:16], private.Name(), probe.Name(), challenge))
			if err != nil {
				t.Fatal(err)
			}
			after, err := client.Status(ctx)
			if err != nil || after.Mode != "closed" {
				t.Fatal("test network unexpectedly opened")
			}
			files, network := s.inspectVMLayout()
			t.Logf("policy files=%t network=%t; shared=%t private=%s process TZ=%s locale=%s system TZ=%s locale=%s; IPv6 default routes=%d connected=%t; UDP attempted=%t; denied tcp/udp/dns counters %d/%d/%d -> %d/%d/%d", files, network, boundary.SharedFile, boundary.PrivateFile, boundary.TZ, boundary.ProcessLocale, boundary.SystemTimezone, boundary.SystemLocale, boundary.IPv6DefaultRoutes, boundary.IPv6Connected, boundary.UDPAttempted, before.Counters.TCPDenied, before.Counters.UDPDenied, before.Counters.DNSDenied, after.Counters.TCPDenied, after.Counters.UDPDenied, after.Counters.DNSDenied)
			if !files || !network || !boundary.SharedFile || boundary.PrivateFile != "unreachable" || boundary.PublicConnected || boundary.ManagementConnected || boundary.PublicDNSAnswered || !boundary.UDPAttempted || after.Counters.UDPDenied <= before.Counters.UDPDenied || after.Counters.DNSDenied <= before.Counters.DNSDenied {
				t.Fatal("guest boundary evidence incomplete")
			}
		}

		if os.Getenv("CAGE_TEST_MACOS_WORKBENCH_EDITOR") == "1" {
			defer s.closeWindowsBrowser()
			doc, err := os.CreateTemp(project, "qa-*.txt")
			if err != nil {
				t.Fatal(err)
			}
			doc.Close()
			defer os.Remove(doc.Name())
			base := filepath.Base(doc.Name())
			code, raw := post("macos-browser/action", map[string]any{"action": "editor", "workspace": project})
			var opened struct {
				OK    bool
				Error string
			}
			if code != 200 || json.Unmarshal([]byte(raw), &opened) != nil || !opened.OK {
				t.Fatalf("guest editor could not open the authorized folder: status=%d error=%q", code, opened.Error)
			}
			code, raw = post("macos-browser/action", map[string]any{"action": "frame"})
			var frame struct {
				URL, Image    string
				Width, Height int
			}
			if err := json.Unmarshal([]byte(raw), &frame); code != 200 || err != nil {
				t.Fatalf("editor frame: status=%d err=%v", code, err)
			}
			guestURL, err := url.Parse(frame.URL)
			img, imgErr := base64.StdEncoding.DecodeString(frame.Image)
			if err != nil || guestURL.Hostname() != filepath.Base(macOSProjectPath(binding.User, project))+".cage-editor.localhost" || guestURL.Query().Get("folder") != macOSProjectPath(binding.User, project) || imgErr != nil || len(img) < 1024 || frame.Width < 1 || frame.Height < 1 {
				t.Fatal("browser did not show the expected guest-local project")
			}

			action := func(body map[string]any) map[string]any {
				t.Helper()
				status, raw := post("macos-browser/action", body)
				var result map[string]any
				if status != 200 || json.Unmarshal([]byte(raw), &result) != nil || result["error"] != nil {
					t.Fatalf("browser %s: %d %v", body["action"], status, result["error"])
				}
				return result
			}
			snapshot := func(name string) map[string]any {
				result := action(map[string]any{"action": "frame"})
				if dir := os.Getenv("CAGE_TEST_MACOS_EDITOR_FRAMES"); dir != "" {
					jpg, _ := result["image"].(string)
					data, err := base64.StdEncoding.DecodeString(jpg)
					if err != nil {
						t.Fatal(err)
					}
					if err = os.WriteFile(filepath.Join(dir, name+".jpg"), data, 0600); err != nil {
						t.Fatal(err)
					}
				}
				return result
			}
			deadline := time.Now().Add(18 * time.Second)
			for {
				state := snapshot("ready")
				title, _ := state["title"].(string)
				if strings.Contains(title, "code-server") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("workspace window did not initialize")
				}
				time.Sleep(450 * time.Millisecond)
			}
			scratch := t.TempDir()
			source := filepath.Join(scratch, "find.swift")
			finder := filepath.Join(scratch, "find")
			if err := os.WriteFile(source, macOSEditorFileLocator, 0600); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command("/usr/bin/xcrun", "swiftc", source, "-o", finder).CombinedOutput(); err != nil {
				t.Fatalf("Vision file locator: %v %s", err, out)
			}
			jpeg := filepath.Join(scratch, "editor.jpg")
			deadline = time.Now().Add(22 * time.Second)
			for {
				state := snapshot("explorer")
				data, err := base64.StdEncoding.DecodeString(state["image"].(string))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(jpeg, data, 0600); err != nil {
					t.Fatal(err)
				}
				result, err := exec.Command(finder, jpeg, base).Output()
				var x, y int
				if _, scan := fmt.Sscanf(string(result), "%d %d", &x, &y); err == nil && scan == nil {
					action(map[string]any{"action": "click", "double": true, "x": x, "y": y})
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("temporary file not visible in the guest editor Explorer")
				}
				time.Sleep(400 * time.Millisecond)
			}

			deadline = time.Now().Add(15 * time.Second)
			for {
				frame := snapshot("file-editor")
				title, _ := frame["title"].(string)
				if strings.Contains(title, base) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("unique test file did not acquire editor focus (title=%q)", title)
				}
				time.Sleep(450 * time.Millisecond)
			}
			marker := "guest-editor-write-" + randomToken()[:12]
			action(map[string]any{"action": "text", "text": marker})
			action(map[string]any{"action": "key", "meta": true, "key": "s"})
			deadline = time.Now().Add(10 * time.Second)
			for {
				content, err := os.ReadFile(doc.Name())
				if err == nil && strings.TrimSpace(string(content)) == marker {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("browser editor save was not observed on host")
				}
				time.Sleep(150 * time.Millisecond)
			}
			t.Log("Selected a unique project file in the guest editor, confirmed its title and read its saved contents back on the host")

			t.Logf("guest-local code-server opened the selected project; JPEG %dx%d, %d bytes", frame.Width, frame.Height, len(img))
		}
		t.Log(expected)
		return
	}
	got := output.String()
	from := strings.Index(got, "CAGE_TRIAL_Darwin_")
	if from < 0 {
		t.Fatal("guest did not report Darwin")
	}
	to := strings.Index(got[from:], "_42_END")
	if to < 0 {
		t.Fatal("guest program output did not match")
	}
	t.Log(got[from : from+to+len("_42_END")])
}

func TestMacOSGuestIPv6LinkDrop(t *testing.T) {
	profile := os.Getenv("CAGE_TEST_MACOS_LINK_PROFILE")
	if profile == "" {
		t.Skip("select a bound, closed macOS test instance")
	}
	binding, err := guestos.LoadMacOSProfile(profile)
	if err != nil || binding == nil {
		t.Fatal("invalid macOS test profile")
	}
	s := &Server{profile: profile, macosGuest: binding}
	client, err := s.gateClient()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	original, err := client.Status(ctx)
	if err != nil || original.Mode != "closed" {
		t.Fatal("test requires a closed gate")
	}
	state, err := client.BeginCheck(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		closed, err := client.Close(closeCtx, "链路验证结束，外网关闭")
		if err != nil || closed.Mode != "closed" {
			t.Error("gate closure could not be verified")
		}
	}()
	// Both IDs come from the selected instance's gate controller. No general
	// diagnostic request can publish observed-frame evidence.
	command := `iface=$(/sbin/route -n get -inet 198.18.0.254 | /usr/bin/awk '/interface:/{print $2}'); [[ "$iface" =~ ^en[0-9]+$ ]] || exit 1; /usr/local/libexec/cage-macos-ipv6-check link "$iface" ` + shellescape.Quote(state.RunID) + " " + shellescape.Quote(state.CheckID)
	out, err := s.guest(ctx, command)
	if err != nil || strings.TrimSpace(string(out)) != "sent" {
		t.Fatalf("guest failed to submit the one-hop datagram: %v", err)
	}
	for i := 0; i < 15; i++ {
		after, err := client.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if after.Mode != state.Mode || after.RunID != state.RunID || after.CheckID != state.CheckID {
			t.Fatal("checking epoch changed during observation")
		}
		if after.IPv6LinkProbeCheck == state.CheckID {
			t.Log("guest sent the live nonce on its VZ link; host IPv4-only stack confirmed the matching ingress drop")
			return
		}
		time.Sleep(120 * time.Millisecond)
	}
	t.Fatal("no matching IPv6 ingress evidence on the current host agent")
}
