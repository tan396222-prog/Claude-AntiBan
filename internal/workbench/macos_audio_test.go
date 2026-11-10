package workbench

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

func TestMacOSAudioUsesBoundTransportAndClosesCapture(t *testing.T) {
	var allowed atomic.Bool
	allowed.Store(true)
	closed := make(chan struct{}, 1)
	pcm := bytes.Repeat([]byte{1, 2, 3, 4}, 960)
	guest := newMacWorkspaceGuest(t, func(channel ssh.Channel, command string) (uint32, bool) {
		if command != macOSWindowSocketCommand {
			return 1, false
		}
		line, err := bufio.NewReader(channel).ReadString('\n')
		if err != nil {
			return 1, true
		}
		switch strings.TrimSpace(line) {
		case `{"action":"audio-status"}`:
			json.NewEncoder(channel).Encode(map[string]any{"supported": true, "available": allowed.Load(), "reason": "screen_recording_permission"})
		case `{"action":"audio-capture"}`:
			fmt.Fprintln(channel, `{"ready":true,"format":"s16le","sample_rate":48000,"channels":2}`)
			channel.Write(pcm)
			io.Copy(io.Discard, channel)
			closed <- struct{}{}
		default:
			return 1, true
		}
		return 0, true
	})
	s := guest.s
	server := httptest.NewServer(s.handler())
	defer server.Close()
	s.origin = server.URL
	headers := http.Header{
		"Origin": {s.origin},
		"Cookie": {s.cookieName() + "=" + s.session + "; " + s.cookieName() + "-desktop=" + trustedViewProof(s.token)},
	}
	statusRequest, _ := http.NewRequest(http.MethodGet, s.origin+"/api/audio", nil)
	statusRequest.Header = headers.Clone()
	response, err := server.Client().Do(statusRequest)
	if err != nil {
		t.Fatal(err)
	}
	var format map[string]any
	decodeErr := json.NewDecoder(response.Body).Decode(&format)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || decodeErr != nil || format["sample_rate"] != float64(48000) || format["channels"] != float64(2) {
		t.Fatalf("native audio status: %d %v", response.StatusCode, decodeErr)
	}
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(s.origin, "http")+"/api/audio", headers)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := ws.ReadJSON(&format); err != nil || format["format"] != "s16le" {
		t.Fatalf("native audio websocket format: %v", err)
	}
	kind, data, err := ws.ReadMessage()
	if err != nil || kind != websocket.BinaryMessage || !bytes.Equal(data, pcm) {
		t.Fatalf("native PCM changed: %v", err)
	}
	ws.Close()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("closed workbench retained its SSH capture")
	}
	allowed.Store(false)
	response, err = server.Client().Do(statusRequest)
	if err != nil {
		t.Fatal(err)
	}
	message, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusConflict || !strings.Contains(string(message), "Cage Guest Window") || !strings.Contains(string(message), "系统声音") {
		t.Fatal("missing guest permission did not return an actionable error")
	}
}
