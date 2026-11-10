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
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/cage"
	"golang.org/x/crypto/ssh"
)

func macPhoneFrame(t *testing.T, id string) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := jpeg.Encode(&data, image.NewRGBA(image.Rect(0, 0, 900, 600)), nil); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"id": id, "document": "captured-geometry", "width": 390, "height": 640,
		"content_width": 900, "content_height": 600, "image": base64.StdEncoding.EncodeToString(data.Bytes()),
		"focus": "real-ax-focus", "shared": true, "login_supported": false, "pixel_ratio_control": true})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestMacOSPhoneDesktopUsesPrivateRPCAndUTF8Envelope(t *testing.T) {
	id := uuid.NewString()
	frame := macPhoneFrame(t, id)
	var mu sync.Mutex
	var calls []macOSWindowRequest
	guest := newMacWorkspaceGuest(t, func(channel ssh.Channel, command string) (uint32, bool) {
		if !strings.Contains(command, "window.sock") {
			return 0, false
		}
		var input macOSWindowRequest
		if err := json.NewDecoder(bufio.NewReader(channel)).Decode(&input); err != nil {
			t.Error(err)
			return 1, true
		}
		mu.Lock()
		calls = append(calls, input)
		mu.Unlock()
		if input.Phone == nil || input.PhoneToken == "" {
			t.Error("phone lease missing")
		}
		if input.Action == "phone-frame" {
			_, _ = channel.Write(frame)
		} else {
			fmt.Fprintf(channel, `{"id":%q}`, id)
		}
		return 0, true
	})
	s := guest.s
	secret := randomToken()
	for _, op := range []string{"open", "frame", "action", "action", "close"} {
		input := phoneSurfaceInput{ID: id, Width: 390, Height: 640}
		if op == "action" {
			input.Type, input.Focus, input.Document = "text", "real-ax-focus", "captured-geometry"
			input.Text = strings.Repeat("\x01", 8192)
			mu.Lock()
			if len(calls) == 3 {
				input.Text = strings.Repeat("中", 2730) + "ab"
			}
			mu.Unlock()
		}
		out, status, err := s.phoneSurfaceGuest(t.Context(), phoneDesktop, secret, op, input)
		if err != nil || status != http.StatusOK || !bytes.Contains(out, []byte(id)) {
			t.Fatalf("%s: %d %v", op, status, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 5 || len(calls[2].Phone.Text) != 8192 || len(calls[3].Phone.Text) != 8192 {
		t.Fatal("8KiB phone text was truncated")
	}
	for _, call := range calls {
		if call.PhoneToken != secret || !strings.HasPrefix(call.Action, "phone-") {
			t.Fatal("phone RPC escaped its lease")
		}
	}
	if validMacOSWindowRequest(macOSWindowRequest{Action: "phone-open"}) {
		t.Fatal("desktop public endpoint admitted phone RPC")
	}
}

func TestMacOSPhoneDesktopRejectsInvalidFramesAndMapsNativeFailures(t *testing.T) {
	id := uuid.NewString()
	frame := macPhoneFrame(t, id)
	for _, code := range []string{"phone_session_ended", "gui_session_changed", "requires_gui_login", "phone_focus_changed", "phone_document_changed", "accessibility_permission", "screen_recording_permission"} {
		want := http.StatusGone
		if strings.Contains(code, "changed") && code != "gui_session_changed" {
			want = http.StatusConflict
		}
		if strings.Contains(code, "permission") {
			want = http.StatusForbidden
		}
		_, status, _ := macOSPhoneDesktopReply([]byte(`{"error":"`+code+`"}`), "frame", id)
		if status != want {
			t.Fatalf("%s: %d", code, status)
		}
	}
	for _, mutation := range []func(map[string]any){
		func(v map[string]any) { v["id"] = uuid.NewString() },
		func(v map[string]any) { v["image"] = "iVBORw0KGgo=" },
		func(v map[string]any) { v["document"] = "" },
		func(v map[string]any) { v["content_width"] = 0 },
		func(v map[string]any) { v["image"] = strings.Repeat("a", 3<<20) },
	} {
		var value map[string]any
		_ = json.Unmarshal(frame, &value)
		mutation(value)
		raw, _ := json.Marshal(value)
		if _, status, _ := macOSPhoneDesktopReply(raw, "frame", id); status != http.StatusBadGateway {
			t.Fatal("invalid frame accepted")
		}
	}
}

func TestMacOSPhoneDesktopHandlerHonorsRoomGateAndRevocation(t *testing.T) {
	id := uuid.NewString()
	frame := macPhoneFrame(t, id)
	var mu sync.Mutex
	var secret string
	guest := newMacWorkspaceGuest(t, func(channel ssh.Channel, command string) (uint32, bool) {
		if !strings.Contains(command, "window.sock") {
			return 0, false
		}
		var input macOSWindowRequest
		if err := json.NewDecoder(channel).Decode(&input); err != nil {
			return 1, true
		}
		mu.Lock()
		defer mu.Unlock()
		if input.Action == "phone-open" {
			secret = input.PhoneToken
		}
		if secret == "" || input.PhoneToken != secret {
			t.Error("host failed to bind private lease")
		}
		if input.Action == "phone-frame" {
			_, _ = channel.Write(frame)
		} else {
			fmt.Fprintf(channel, `{"id":%q}`, id)
		}
		return 0, true
	})
	s := guest.s
	room, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.phones.record = &phoneRecord{}
	s.phones.roomCtx, s.phones.roomProof = room, "current-room"
	open := fmt.Sprintf(`{"id":%q,"width":390,"height":640}`, id)
	if response := windowsPhoneRequest(t, s, "POST", "/api/mobile/desktop/open", open, "old-room"); response.Code != 403 {
		t.Fatalf("old room accepted: %d", response.Code)
	}
	if response := windowsPhoneRequest(t, s, "POST", "/api/mobile/desktop/open", open, "current-room"); response.Code != 409 {
		t.Fatalf("closed gate accepted: %d %s", response.Code, response.Body.String())
	}
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
	defer stop()
	if err := gate.BeginCheck(); err != nil {
		t.Fatal(err)
	}
	state := gate.Status()
	report := cage.SafetyReport{Policy: cage.SafetyPolicyVersion, ID: "phone-fixture", RunID: state.RunID, CheckID: state.CheckID, Configuration: state.Configuration, CreatedAt: time.Now()}
	for _, check := range []string{"egress_policy", "proxy_transport", "dns_transport", "guest_settings", "browser_settings", "filesystem_boundary", "management_boundary", "fail_closed", "udp_policy", "ipv6_policy", "exit_consistency"} {
		report.Checks = append(report.Checks, cage.SafetyCheck{ID: check, Title: check, Critical: true, State: cage.CheckPassed, Evidence: "isolated fixture", CheckedAt: report.CreatedAt})
	}
	if err := gate.Grant(report); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/mobile/desktop/open", "/api/mobile/desktop/" + id + "/frame"} {
		method, body := "GET", ""
		if strings.HasSuffix(path, "open") {
			method, body = "POST", open
		}
		response := windowsPhoneRequest(t, s, method, path, body, "current-room")
		if response.Code != 200 {
			t.Fatalf("native handler: %d %s", response.Code, response.Body.String())
		}
	}
	s.account.trustedSession = true
	if response := windowsPhoneRequest(t, s, "GET", "/api/mobile/desktop/"+id+"/frame", "", "current-room"); response.Code != 423 {
		t.Fatal("desktop-only unlock leaked to phone")
	}
	s.account.trustedSession = false
	cancel()
	if response := windowsPhoneRequest(t, s, "GET", "/api/mobile/desktop/"+id+"/frame", "", "current-room"); response.Code != 403 {
		t.Fatal("revoked phone retained frames")
	}
}
