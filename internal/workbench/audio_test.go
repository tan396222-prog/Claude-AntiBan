package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestAudioBoundary(t *testing.T) {
	s := testServer(t)
	if w := request(s, "GET", "/api/audio", "", "", false); w.Code != 401 {
		t.Fatal("audio allowed without session", w.Code)
	}
	s.account.lock()
	if w := request(s, "GET", "/api/audio", "", "", true); w.Code != 423 {
		t.Fatal("audio allowed while locked", w.Code)
	}
	r := httptest.NewRequest("GET", s.origin+"/api/audio", nil)
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Origin", "https://other.example")
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin audio allowed", w.Code)
	}
}

func TestAudioStreamsWholeFramesAndClosesOnCancellation(t *testing.T) {
	s := testServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	finished := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		s.streamAudio(w, r.WithContext(ctx), reader)
	}))
	defer server.Close()
	s.origin = server.URL
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), http.Header{"Origin": {server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_, header, err := ws.ReadMessage()
	var format map[string]any
	if err != nil || json.Unmarshal(header, &format) != nil || format["sample_rate"] != float64(48000) {
		t.Fatal("missing audio format", err)
	}
	packet := bytes.Repeat([]byte{0, 128, 255, 127}, 960)
	go func() { _, _ = writer.Write(packet[:13]); _, _ = writer.Write(packet[13:]) }()
	kind, data, err := ws.ReadMessage()
	if err != nil || kind != websocket.BinaryMessage || !bytes.Equal(data, packet) {
		t.Fatal("PCM frame changed", err)
	}
	cancel()
	_ = ws.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err = ws.ReadMessage(); err == nil {
		t.Fatal("cancelled stream stayed open")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("audio handler leaked")
	}
}
