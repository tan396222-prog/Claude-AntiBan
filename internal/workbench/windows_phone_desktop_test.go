package workbench

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/guestos"
)

// Execute through the production profile-bound relay command. The fixture is a
// protocol peer only and does not represent Windows capture or UI Automation.
func init() {
	directory := os.Getenv("CAGE_TEST_WINDOWS_PHONE_WINDOW")
	if directory == "" {
		return
	}
	if len(os.Args) != 4 || os.Args[1] != "--relay-profile" || os.Args[3] != "desktop" {
		os.Exit(2)
	}
	capture, _ := os.OpenFile(filepath.Join(directory, "requests.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	defer capture.Close()
	reader := bufio.NewScanner(os.Stdin)
	reader.Buffer(make([]byte, 65536), 128<<10)
	width, height := 800, 600
	for reader.Scan() {
		var input map[string]any
		if json.Unmarshal(reader.Bytes(), &input) != nil {
			os.Exit(3)
		}
		_, _ = capture.Write(append(append([]byte{}, reader.Bytes()...), '\n'))
		control := map[string]any{}
		if data, err := os.ReadFile(filepath.Join(directory, "control.json")); err == nil {
			_ = json.Unmarshal(data, &control)
		}
		target := "window-geometry"
		if changed, ok := control["target"].(string); ok {
			target = changed
		}
		reply := map[string]any{"ok": true}
		switch input["action"] {
		case "status":
			reply = map[string]any{"installed": control["missing"] != true, "running": control["stopped"] != true, "phone_supported": control["legacy"] != true, "display_scale": 1.25}
		case "resize":
			width, height = max(800, int(input["width"].(float64))), max(600, int(input["height"].(float64)))
		case "frame":
			sequence := fmt.Sprintf("%s-%dx%d", target, width, height)
			if input["after"] == sequence {
				reply = map[string]any{"unchanged": true, "sequence": sequence}
			} else {
				var encoded bytes.Buffer
				_ = png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, width, height)))
				reply = map[string]any{"target": target, "width": width, "height": height, "density": 1.25, "sequence": sequence, "png": base64.StdEncoding.EncodeToString(encoded.Bytes())}
			}
		case "ime":
			reply = map[string]any{"available": control["no_focus"] != true, "context": "editable-focus", "password": control["password"] == true}
		case "pointer", "key", "text":
			if input["target"] != target {
				reply = map[string]any{"error": "desktop_target_changed"}
			}
			if input["phone_focus"] != nil && (control["no_focus"] == true || control["password"] == true || input["phone_focus"] != "editable-focus") {
				reply = map[string]any{"error": "desktop_phone_focus_changed"}
			}
		}
		if control["locked"] == true {
			reply = map[string]any{"error": "requires_gui_login"}
		}
		_ = json.NewEncoder(os.Stdout).Encode(reply)
	}
	_ = os.WriteFile(filepath.Join(directory, "closed"), []byte("relay closed"), 0600)
	os.Exit(0)
}

func windowsPhoneDesktopFixture(t *testing.T) (*Server, string, *cage.EgressGate) {
	t.Helper()
	s := testServer(t)
	directory := t.TempDir()
	binding := guestos.WindowsProfile{Version: 1, VMID: uuid.New(), GUIUserSID: "S-1-5-21-1-2-3-1001", GUIUser: "Guest", InstanceDirectory: filepath.Join(s.profile, "unused-vm"), PairingFile: filepath.Join(s.profile, "unused-pair"), RelayExecutable: os.Args[0]}
	if err := guestos.BindWindowsProfile(s.profile, binding); err != nil {
		t.Fatal(err)
	}
	s.windowsGuest = &binding
	if err := os.MkdirAll(binding.InstanceDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	s.ready = true
	room, cancel := context.WithCancel(t.Context())
	s.phones.roomCtx, s.phones.roomProof = room, "phone-window-room"
	s.phones.serving = cancel
	s.phones.record = &phoneRecord{}
	t.Setenv("CAGE_TEST_WINDOWS_PHONE_WINDOW", directory)
	t.Cleanup(func() {
		cancel()
		s.phones.desktop.op.Lock()
		defer s.phones.desktop.op.Unlock()
		s.retirePhoneSurface(phoneDesktop, s.phones.desktop.session)
	})
	cfg, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := cage.NewEgressGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	stop, err := cage.StartGateControl(s.configPath, s.instanceDir(), gate)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	if err := gate.BeginCheck(); err != nil {
		t.Fatal(err)
	}
	state := gate.Status()
	report := cage.SafetyReport{Policy: cage.SafetyPolicyVersion, ID: "windows-phone-test", RunID: state.RunID, CheckID: state.CheckID, Configuration: state.Configuration, CreatedAt: time.Now()}
	for _, check := range []string{"egress_policy", "proxy_transport", "dns_transport", "guest_settings", "browser_settings", "filesystem_boundary", "management_boundary", "fail_closed", "udp_policy", "ipv6_policy", "exit_consistency"} {
		report.Checks = append(report.Checks, cage.SafetyCheck{ID: check, Critical: true, State: cage.CheckPassed, Evidence: "isolated fixture", CheckedAt: report.CreatedAt})
	}
	if err := gate.Grant(report); err != nil {
		t.Fatal(err)
	}
	return s, directory, gate
}

func TestWindowsPhoneDesktopProductionHandlerSharesWindowAndGuardsInput(t *testing.T) {
	s, directory, _ := windowsPhoneDesktopFixture(t)
	id := uuid.NewString()
	base := "/api/mobile/desktop/" + id
	open := fmt.Sprintf(`{"id":%q,"width":390,"height":640,"pixel_ratio":2}`, id)
	if got := windowsPhoneRequest(t, s, "POST", "/api/mobile/desktop/open", open, "old-room"); got.Code != 403 {
		t.Fatalf("old room: %d", got.Code)
	}
	if got := windowsPhoneRequest(t, s, "POST", "/api/mobile/desktop/open", open, "phone-window-room"); got.Code != 200 {
		t.Fatalf("open: %d %s", got.Code, got.Body.String())
	}
	readFrame := func() map[string]any {
		t.Helper()
		got := windowsPhoneRequest(t, s, "GET", base+"/frame", "", "phone-window-room")
		var frame map[string]any
		if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &frame) != nil {
			t.Fatalf("frame: %d %s", got.Code, got.Body.String())
		}
		return frame
	}
	frame := readFrame()
	if frame["shared"] != true || frame["login_supported"] != false || frame["pixel_ratio_supported"] != true || frame["focus"] != "editable-focus" || frame["content_width"] != float64(640) || frame["content_height"] != float64(640) {
		t.Fatalf("shared frame contract: %v", frame)
	}
	encoded, err := base64.StdEncoding.DecodeString(frame["image"].(string))
	if err != nil || len(encoded) > 2<<20 {
		t.Fatal("invalid encoded image")
	}
	geometry, err := jpeg.DecodeConfig(bytes.NewReader(encoded))
	if err != nil || geometry.Width > 780 || geometry.Height > 1280 {
		t.Fatalf("JPEG geometry: %+v %v", geometry, err)
	}
	if cached := readFrame(); cached["image"] != frame["image"] {
		t.Fatal("static frame cache changed")
	}
	if err := os.WriteFile(filepath.Join(directory, "control.json"), []byte(`{"password":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if password := readFrame(); password["focus"] != "" || password["password"] != true {
		t.Fatal("password field accepted phone typing")
	}
	if err := os.WriteFile(filepath.Join(directory, "control.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	readFrame()
	post := func(body map[string]any, want int) {
		t.Helper()
		data, _ := json.Marshal(body)
		got := windowsPhoneRequest(t, s, "POST", base+"/action", string(data), "phone-window-room")
		if got.Code != want {
			t.Fatalf("action %v: %d %s", body["type"], got.Code, got.Body.String())
		}
	}
	post(map[string]any{"type": "tap", "document": "old-geometry", "x": 10, "y": 10}, 409)
	post(map[string]any{"type": "tap", "document": frame["document"], "x": 639, "y": 639}, 200)
	post(map[string]any{"type": "scroll", "document": frame["document"], "x": 400, "y": 400, "dx": -120, "dy": 240}, 200)
	post(map[string]any{"type": "text", "document": frame["document"], "focus": "old-focus", "text": "ignored"}, 409)
	post(map[string]any{"type": "text", "document": frame["document"], "focus": frame["focus"], "text": strings.Repeat("中", 2730) + "ab"}, 200)
	post(map[string]any{"type": "key", "document": frame["document"], "focus": frame["focus"], "key": "Enter"}, 200)
	post(map[string]any{"type": "resize", "width": 800, "height": 600, "pixel_ratio": 1.5}, 200)
	resized := readFrame()
	if resized["width"] != float64(800) || resized["content_width"] != float64(800) || resized["requested_pixel_ratio"] != 1.5 {
		t.Fatal("resize contract lost")
	}
	// The editable focus changes after the host's latest frame. The guest must
	// reject input even though the phone supplied the previously valid token.
	if err := os.WriteFile(filepath.Join(directory, "control.json"), []byte(`{"no_focus":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	post(map[string]any{"type": "text", "document": resized["document"], "focus": resized["focus"], "text": "must reject"}, 409)
	requests, _ := os.ReadFile(filepath.Join(directory, "requests.jsonl"))
	if !bytes.Contains(requests, []byte(`"phone_focus":"editable-focus"`)) || !bytes.Contains(requests, []byte(`"mask":1`)) || !bytes.Contains(requests, []byte(`"mask":0`)) {
		t.Fatal("native input guard or release missing")
	}
}

func TestWindowsPhoneDesktopReadinessAndCapabilities(t *testing.T) {
	for _, test := range []struct {
		name, control string
		want          int
	}{{"legacy", `{"legacy":true}`, 426}, {"missing", `{"missing":true}`, 501}, {"locked", `{"locked":true}`, 410}} {
		t.Run(test.name, func(t *testing.T) {
			s, directory, _ := windowsPhoneDesktopFixture(t)
			_ = os.WriteFile(filepath.Join(directory, "control.json"), []byte(test.control), 0600)
			body := fmt.Sprintf(`{"id":%q,"width":390,"height":640}`, uuid.NewString())
			got := windowsPhoneRequest(t, s, "POST", "/api/mobile/desktop/open", body, "phone-window-room")
			if got.Code != test.want {
				t.Fatalf("%s: %d %s", test.name, got.Code, got.Body.String())
			}
			if test.name == "locked" && !strings.Contains(got.Body.String(), "Windows 客机账户") {
				t.Fatal("locked GUI did not explain how to recover")
			}
			if s.phones.desktop.nativeDesktop != nil || s.phones.desktop.session != nil {
				t.Fatal("failed open retained native lease")
			}
		})
	}
	s, _, gate := windowsPhoneDesktopFixture(t)
	s.windowsState = windowsGuestState{Available: true, ComponentsChecked: time.Now(), Capabilities: []string{"desktop"}, Components: map[string]windowsComponentState{"phone-desktop": {Supported: true, Registered: true, Installed: true, Ready: true}}}
	status := gate.Status()
	s.safety.Gate = &status
	if !s.windowsCapabilitiesLocked()["available"].(map[string]bool)["phone-desktop"] {
		t.Fatal("ready supported desktop unavailable")
	}
	s.networkPaused = true
	if s.windowsCapabilitiesLocked()["available"].(map[string]bool)["phone-desktop"] {
		t.Fatal("paused network advertised phone desktop")
	}
	s.networkPaused = false
	s.windowsState.Available = false
	if s.windowsCapabilitiesLocked()["available"].(map[string]bool)["phone-desktop"] {
		t.Fatal("locked GUI advertised phone desktop")
	}
}

func TestWindowsPhoneDesktopRevocationClosesNativeRelay(t *testing.T) {
	s, directory, _ := windowsPhoneDesktopFixture(t)
	id := uuid.NewString()
	body := fmt.Sprintf(`{"id":%q,"width":390,"height":640}`, id)
	got := windowsPhoneRequest(t, s, "POST", "/api/mobile/desktop/open", body, "phone-window-room")
	if got.Code != 200 {
		t.Fatalf("open: %d %s", got.Code, got.Body.String())
	}
	native := s.phones.desktop.nativeDesktop
	s.stopPhone()
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.phones.desktop.op.Lock()
		closed := s.phones.desktop.session == nil && s.phones.desktop.nativeDesktop == nil
		s.phones.desktop.op.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("revocation retained the native relay")
		}
		time.Sleep(time.Millisecond)
	}
	if native.connection.cmd.ProcessState == nil {
		t.Fatal("revocation did not reap the relay process")
	}
	if got := windowsPhoneRequest(t, s, "GET", "/api/mobile/desktop/"+id+"/frame", "", "phone-window-room"); got.Code != 403 {
		t.Fatalf("revoked room: %d", got.Code)
	}
	requests, _ := os.ReadFile(filepath.Join(directory, "requests.jsonl"))
	if bytes.Contains(requests, []byte(`"action":"stop"`)) {
		t.Fatal("phone close stopped the shared desktop application")
	}
}
