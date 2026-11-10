package workbench

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net"
	"testing"
	"time"
)

func TestNativeTightPreservesJPEGAndInputAfterUnchangedFrame(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 64; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 200, G: 90, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	frame := windowsWindowFrame{JPEG: base64.StdEncoding.EncodeToString(encoded.Bytes()), Width: 64, Height: 48, Target: "window", Sequence: "first"}
	server, client := net.Pipe()
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	keys := make(chan map[string]any, 1)
	go func() {
		defer server.Close()
		_ = serveWindowsWindowRFB(server, frame, func(input map[string]any, output any) error {
			if input["action"] == "key" {
				keys <- input
				return nil
			}
			if input["after"] == "first" {
				*output.(*windowsWindowFrame) = windowsWindowFrame{Sequence: "first", Unchanged: true}
			} else {
				*output.(*windowsWindowFrame) = frame
			}
			return nil
		})
	}()
	read := func(n int) []byte {
		t.Helper()
		data := make([]byte, n)
		if _, err := io.ReadFull(client, data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	write := func(data []byte) {
		t.Helper()
		if _, err := client.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	read(12)
	write([]byte("RFB 003.008\n"))
	read(2)
	write([]byte{1})
	read(4)
	write([]byte{1})
	init := read(24)
	read(int(binary.BigEndian.Uint32(init[20:])))
	write([]byte{2, 0, 0, 1, 0, 0, 0, 7})
	write([]byte{3, 0, 0, 0, 0, 0, 0, 64, 0, 48})
	header := read(16)
	if binary.BigEndian.Uint32(header[12:]) != 7 || read(1)[0] != 0x90 {
		t.Fatal("JPEG did not use Tight passthrough")
	}
	length := 0
	for shift := 0; shift <= 14; shift += 7 {
		value := read(1)[0]
		if shift == 14 {
			length |= int(value) << shift
			break
		}
		length |= int(value&127) << shift
		if value&128 == 0 {
			break
		}
	}
	if !bytes.Equal(read(length), encoded.Bytes()) {
		t.Fatal("JPEG was re-encoded")
	}
	write([]byte{3, 1, 0, 0, 0, 0, 0, 64, 0, 48})
	if !bytes.Equal(read(4), []byte{0, 0, 0, 0}) {
		t.Fatal("unchanged frame resent pixels")
	}
	write([]byte{4, 1, 0, 0, 0, 0, 0, 'a'})
	select {
	case input := <-keys:
		if input["target"] != "window" {
			t.Fatal("unchanged frame lost the input target")
		}
	case <-time.After(time.Second):
		t.Fatal("input stalled after Tight frame")
	}
}

func TestNativeTightPNGPreservesEncodedWindow(t *testing.T) {
	frame := windowTestFrame(t, 64, 48)
	encoded, err := base64.StdEncoding.DecodeString(frame.PNG)
	if err != nil {
		t.Fatal(err)
	}
	rectangles, count, supported, err := nativeTightRectangles(frame, -260)
	if err != nil || !supported || count != 1 || int32(binary.BigEndian.Uint32(rectangles[8:12])) != -260 || rectangles[12] != 0xa0 {
		t.Fatalf("PNG passthrough failed: supported=%v count=%d error=%v", supported, count, err)
	}
	if !bytes.HasSuffix(rectangles, encoded) {
		t.Fatal("PNG changed during transport")
	}
	_, _, supported, err = nativeTightRectangles(frame, 7)
	if err != nil || supported {
		t.Fatal("PNG sent to a JPEG-only Tight client")
	}
}
