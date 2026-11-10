package workbench

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func macFrameFixture(t *testing.T) (macOSFrameHeader, []byte) {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 64, 48))); err != nil {
		t.Fatal(err)
	}
	return macOSFrameHeader{ImageLength: data.Len(), MIME: "image/png", Sequence: "first", Geometry: "geometry", Window: 7, PID: 23,
		ContentWidth: 32, ContentHeight: 24, PixelWidth: 64, PixelHeight: 48}, data.Bytes()
}

func macFrameRecord(t *testing.T, header macOSFrameHeader, pixels []byte) []byte {
	t.Helper()
	metadata, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, 4+len(metadata)+len(pixels))
	binary.BigEndian.PutUint32(packet, uint32(len(metadata)))
	copy(packet[4:], metadata)
	copy(packet[4+len(metadata):], pixels)
	return packet
}

func TestMacOSBinaryFramePreservesBytesAndRejectsInvalidRecords(t *testing.T) {
	header, pixels := macFrameFixture(t)
	decoded, actual, err := readMacOSFrame(bytes.NewReader(macFrameRecord(t, header, pixels)))
	if err != nil || decoded != header || !bytes.Equal(actual, pixels) {
		t.Fatalf("frame changed in transit: %v", err)
	}
	for _, mutation := range []func(*macOSFrameHeader){
		func(h *macOSFrameHeader) { h.ImageLength = 8 << 20 },
		func(h *macOSFrameHeader) { h.ImageLength++ },
		func(h *macOSFrameHeader) { h.PixelWidth++ },
		func(h *macOSFrameHeader) { h.MIME = "image/jpeg" },
		func(h *macOSFrameHeader) { h.Unchanged = true },
		func(h *macOSFrameHeader) { h.Geometry = "" },
	} {
		invalid := header
		mutation(&invalid)
		if _, _, err := readMacOSFrame(bytes.NewReader(macFrameRecord(t, invalid, pixels))); err == nil {
			t.Fatalf("accepted invalid frame metadata %+v", invalid)
		}
	}
	if _, _, err := readMacOSFrame(bytes.NewReader([]byte{0, 1, 0, 0})); err == nil {
		t.Fatal("accepted an oversized header")
	}
}

func TestMacOSFrameStreamAcknowledgementResizeAndDisconnect(t *testing.T) {
	s := testServer(t)
	transport, guest := net.Pipe()
	defer guest.Close()
	requests := make(chan macOSWindowRequest, 4)
	responses := make(chan []byte, 4)
	guestDone := make(chan struct{})
	go func() {
		defer close(guestDone)
		scanner := bufio.NewScanner(guest)
		for scanner.Scan() {
			var request macOSWindowRequest
			if json.Unmarshal(scanner.Bytes(), &request) != nil {
				return
			}
			requests <- request
			response, ok := <-responses
			if !ok {
				return
			}
			if _, err := guest.Write(response); err != nil {
				return
			}
		}
	}()
	defer func() { close(responses); guest.Close(); <-guestDone }()
	finished := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		ws, err := s.upgrade(w, r)
		if err != nil {
			return
		}
		defer ws.Close()
		relayMacOSFrames(r.Context(), ws, transport, transport)
	}))
	defer server.Close()
	s.origin = server.URL
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), http.Header{"Origin": {server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	send := func(message any) {
		t.Helper()
		if err := ws.WriteJSON(message); err != nil {
			t.Fatal(err)
		}
	}
	next := func() macOSWindowRequest {
		t.Helper()
		select {
		case request := <-requests:
			if !request.Wait || !request.Binary || !request.KeepAlive || !request.Refresh {
				t.Fatalf("frame stream lost its wire contract: %+v", request)
			}
			return request
		case <-time.After(2 * time.Second):
			t.Fatal("guest did not receive a request")
			return macOSWindowRequest{}
		}
	}
	read := func() macOSFrameHeader {
		t.Helper()
		_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
		kind, record, err := ws.ReadMessage()
		if err != nil || kind != websocket.BinaryMessage {
			t.Fatalf("frame response: %v", err)
		}
		header, _, err := readMacOSFrame(bytes.NewReader(record))
		if err != nil {
			t.Fatal(err)
		}
		return header
	}
	send(macOSFrameSubscription{Width: 800, Height: 600, Scale: 2, Revision: 1})
	if first := next(); first.After != "" || first.Width != 800 {
		t.Fatalf("bad first request: %+v", first)
	}
	header, pixels := macFrameFixture(t)
	responses <- macFrameRecord(t, header, pixels)
	if read().Revision != 1 {
		t.Fatal("lost subscription revision")
	}
	select {
	case <-requests:
		t.Fatal("captured another image before decode acknowledgement")
	case <-time.After(100 * time.Millisecond):
	}
	send(macOSFrameSubscription{Ack: "first"})
	if next().After != "first" {
		t.Fatal("lost the consumed frame sequence")
	}
	responses <- macFrameRecord(t, macOSFrameHeader{Unchanged: true, Sequence: "first"}, nil)
	next()
	// Resize while the guest is waiting for the next changed frame. The old
	// response must be consumed without forwarding it to the new viewport.
	send(macOSFrameSubscription{Width: 900, Height: 640, Scale: 2, Revision: 2})
	time.Sleep(50 * time.Millisecond)
	header.Sequence = "old-size"
	responses <- macFrameRecord(t, header, pixels)
	resized := next()
	if resized.Width != 900 || resized.After != "" {
		t.Fatalf("stale resize request: %+v", resized)
	}
	header.Sequence = "new-size"
	responses <- macFrameRecord(t, header, pixels)
	if result := read(); result.Sequence != "new-size" || result.Revision != 2 {
		t.Fatalf("forwarded a stale or unchanged frame: %+v", result)
	}
	send(macOSFrameSubscription{Ack: "new-size"})
	next()
	ws.Close()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("disconnect left a blocked guest read")
	}
}
