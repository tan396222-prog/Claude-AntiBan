package workbench

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

func TestMacOSWindowInputContract(t *testing.T) {
	x, y, button, down := 0.2, 0.3, 2, true
	target := macOSWindowRequest{PID: 42, Window: 7, Geometry: "geometry"}
	pointer := target
	pointer.Action, pointer.Phase, pointer.X, pointer.Y = "pointer", "down", &x, &y
	pointer.Button, pointer.Buttons, pointer.Clicks = &button, 2, 2
	if !validMacOSWindowRequest(pointer) {
		t.Fatal("targeted right double-click was rejected")
	}
	pointer.Phase, pointer.Button = "move", nil
	if !validMacOSWindowRequest(pointer) {
		t.Fatal("targeted drag move was rejected")
	}
	pointer.Geometry = ""
	if validMacOSWindowRequest(pointer) {
		t.Fatal("pointer without captured geometry was accepted")
	}
	key := target
	key.Action, key.Key, key.Code, key.Down = "key", "/", "Slash", &down
	if !validMacOSWindowRequest(key) {
		t.Fatal("physical key transition was rejected")
	}
	key.Down = nil
	if validMacOSWindowRequest(key) {
		t.Fatal("physical key without a transition was accepted")
	}
	ime := target
	ime.Action, ime.Mode, ime.Context, ime.Session = "ime", "begin", strings.Repeat("a", 32), strings.Repeat("b", 32)
	if !validMacOSWindowRequest(ime) {
		t.Fatal("targeted composition was rejected")
	}
	ime.Mode, ime.Sequence = "commit", -1
	if validMacOSWindowRequest(ime) {
		t.Fatal("negative composition sequence was accepted")
	}
	if !validMacOSWindowRequest(macOSWindowRequest{Action: "input-reset"}) || !validMacOSWindowRequest(macOSWindowRequest{Action: "status"}) {
		t.Fatal("connection cleanup or existing status request was rejected")
	}
}

func TestMacOSWindowInputRoutesPreserveCompositionSequence(t *testing.T) {
	guest := newMacWorkspaceGuest(t, func(channel ssh.Channel, command string) (uint32, bool) {
		if !strings.Contains(command, "window.sock") {
			return 0, false
		}
		reader := bufio.NewReader(channel)
		for {
			var input macOSWindowRequest
			if json.NewDecoder(reader).Decode(&input) != nil {
				return 0, true
			}
			if json.NewEncoder(channel).Encode(input) != nil || !input.KeepAlive {
				return 0, true
			}
		}
	})
	s := guest.s
	server := httptest.NewServer(http.HandlerFunc(s.macOSWindowConnection))
	defer server.Close()
	s.origin = server.URL
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), http.Header{"Origin": {server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	for sequence, mode := range []string{"begin", "update", "commit"} {
		input := macOSWindowRequest{Action: "ime", PID: 42, Window: 7, Geometry: "geometry", Mode: mode,
			Context: strings.Repeat("a", 32), Session: strings.Repeat("b", 32), Sequence: sequence,
			Project: "ungranted", PhoneToken: "ungranted", Path: "ungranted", Transfer: "ungranted", IDs: []string{"ungranted"}, KeepAlive: true, Wait: true, Binary: true}
		if mode == "commit" {
			input.Text = strings.Repeat("\x01", 8192)
		}
		if err := ws.WriteJSON(map[string]any{"id": sequence + 1, "request": input}); err != nil {
			t.Fatal(err)
		}
		var response struct {
			ID     int                `json:"id"`
			Result macOSWindowRequest `json:"result"`
		}
		_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		if err := ws.ReadJSON(&response); err != nil {
			t.Fatal(err)
		}
		check := func(actual macOSWindowRequest, keepAlive bool) {
			t.Helper()
			if actual.Sequence != sequence || actual.Session != input.Session || actual.Text != input.Text || actual.Geometry != input.Geometry {
				t.Fatalf("composition changed in transit for %s, sequence %d", mode, actual.Sequence)
			}
			if actual.Project != "" || actual.Path != "" || actual.PhoneToken != "" || actual.Transfer != "" || actual.IDs != nil || actual.Wait || actual.Binary || actual.KeepAlive != keepAlive {
				t.Fatalf("public input retained privileged fields for %s", mode)
			}
		}
		check(response.Result, true)
		data, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/macos/claude/window", bytes.NewReader(data))
		request.Header.Set("Content-Type", "application/json")
		s.macOSClaudeWindow(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("HTTP input returned %d: %s", recorder.Code, recorder.Body.String())
		}
		var actual macOSWindowRequest
		if err := json.Unmarshal(recorder.Body.Bytes(), &actual); err != nil {
			t.Fatal(err)
		}
		check(actual, false)
	}
}
