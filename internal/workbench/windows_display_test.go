package workbench

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"testing"
	"time"
)

func TestWindowsRFBMatchesNoVNCPixelFormatAndZlibStream(t *testing.T) {
	frame := windowTestFrame(t, 64, 64)
	server, client := net.Pipe()
	defer client.Close()
	done := make(chan error, 1)
	go func() {
		defer server.Close()
		done <- serveWindowsWindowRFB(server, frame, func(input map[string]any, output any) error { *output.(*windowsWindowFrame) = frame; return nil })
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
	// This is the actual full-depth format requested by the bundled noVNC:
	// little endian, with red in the first byte and blue in the third.
	format := make([]byte, 20)
	copy(format[4:], []byte{32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 0, 8, 16})
	write(format)
	write([]byte{2, 0, 0, 1, 0, 0, 0, 6})
	var compressed bytes.Buffer
	for i := 0; i < 2; i++ {
		write([]byte{3, 0, 0, 0, 0, 0, 0, 64, 0, 64})
		header := read(16)
		if int32(binary.BigEndian.Uint32(header[12:])) != 6 {
			t.Fatal("noVNC's supported compressed encoding was not used")
		}
		size := int(binary.BigEndian.Uint32(read(4)))
		if size >= 64*64*4 {
			t.Fatal("static application frame remained uncompressed")
		}
		compressed.Write(read(size))
	}
	decoder, err := zlib.NewReader(&compressed)
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	for i := 0; i < 2; i++ {
		pixels := make([]byte, 64*64*4)
		if _, err := io.ReadFull(decoder, pixels); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(pixels[:4], []byte{240, 110, 30, 0}) {
			t.Fatal("noVNC would display swapped colors")
		}
	}
	client.Close()
	<-done
}

func TestWindowsGuestCommandKeyMapping(t *testing.T) {
	for _, key := range []uint32{0xffe7, 0xffe8, 0xffeb, 0xffec} {
		if windowsGuestKeysym(key, "mac") != 0xffe3 || windowsGuestKeysym(key, "windows") != key {
			t.Fatal("host Command mapping changed the Windows host key")
		}
	}
	for _, key := range []uint32{0xffe3, 0xffe4, 0xffe9, 'c'} {
		if windowsGuestKeysym(key, "mac") != key {
			t.Fatal("non-Command key changed")
		}
	}
}

func windowTestFrame(t *testing.T, width, height int) windowsWindowFrame {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 240, G: 110, B: 30, A: 255})
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return windowsWindowFrame{PNG: base64.StdEncoding.EncodeToString(out.Bytes()), Width: width, Height: height, Target: "target"}
}

func TestWindowsWindowRFBUsesRealFrameAndInputTarget(t *testing.T) {
	frame := windowTestFrame(t, 2, 1)
	server, client := net.Pipe()
	defer client.Close()
	if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	events := make(chan map[string]any, 8)
	go func() {
		defer server.Close()
		done <- serveWindowsWindowRFB(server, frame, func(input map[string]any, output any) error {
			if input["action"] == "frame" {
				*output.(*windowsWindowFrame) = frame
			} else {
				events <- input
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
	if string(read(12)) != "RFB 003.008\n" {
		t.Fatal("missing RFB version")
	}
	write([]byte("RFB 003.008\n"))
	if !bytes.Equal(read(2), []byte{1, 1}) {
		t.Fatal("wrong security")
	}
	write([]byte{1})
	read(4)
	write([]byte{1})
	init := read(24)
	read(int(binary.BigEndian.Uint32(init[20:])))
	if binary.BigEndian.Uint16(init) != 2 || binary.BigEndian.Uint16(init[2:]) != 1 {
		t.Fatal("initial dimensions lost")
	}
	format := make([]byte, 20)
	copy(format[4:], []byte{32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 16, 8, 0})
	write(format)
	write([]byte{2, 0, 0, 2, 255, 255, 255, 33, 255, 255, 254, 200}) // DesktopSize and Fence
	notice := []byte{248, 0, 0, 0, 0x80, 0, 0, 0, 0}
	if !bytes.Equal(read(9), notice) {
		t.Fatal("client cannot enable ordered text input")
	}
	notice[4] = 0
	write(notice)
	write([]byte{3, 0, 0, 0, 0, 0, 0, 2, 0, 1})
	update := read(16)
	if binary.BigEndian.Uint16(update[2:]) != 1 || binary.BigEndian.Uint16(update[8:]) != 2 {
		t.Fatal("frame update metadata lost")
	}
	pixels := read(8)
	if !bytes.Equal(pixels[:4], []byte{30, 110, 240, 0}) {
		t.Fatalf("pixel order changed: %v", pixels)
	}
	write([]byte{5, 1, 0, 1, 0, 0})
	pointer := <-events
	if pointer["target"] != "target" || pointer["x"] != uint16(1) || pointer["mask"] != byte(1) {
		t.Fatal("pointer target or coordinates changed")
	}
	write([]byte{4, 1, 0, 0, 0, 0, 0, 97})
	key := <-events
	if key["keysym"] != uint32(97) || key["down"] != true || key["target"] != "target" {
		t.Fatal("key event lost")
	}
	payload := []byte("cage-input-1")
	fence := append([]byte{248, 0, 0, 0, 0x80, 0, 0, 1, byte(len(payload))}, payload...)
	write(fence)
	fence[4] = 0
	if !bytes.Equal(read(len(fence)), fence) {
		t.Fatal("input acknowledgement did not preserve the ordered request")
	}
	// The native geometry changes, then the next frame updates the existing view.
	frame = windowTestFrame(t, 3, 1)
	write([]byte{3, 1, 0, 0, 0, 0, 0, 2, 0, 1})
	update = read(28)
	if binary.BigEndian.Uint16(update[2:]) != 2 || int32(binary.BigEndian.Uint32(update[12:])) != -223 || binary.BigEndian.Uint16(update[8:]) != 3 {
		t.Fatal("existing RFB view did not resize")
	}
	read(12)
	client.Close()
	<-done
}

func TestWindowsFrameRejectsMismatchedDimensions(t *testing.T) {
	frame := windowTestFrame(t, 2, 2)
	frame.Width = 8192
	frame.Height = 8192
	if _, err := nativeFramePixels(frame); err == nil {
		t.Fatal("oversized image accepted")
	}
	frame.Width = 3
	frame.Height = 2
	if _, err := nativeFramePixels(frame); err == nil {
		t.Fatal("reported geometry differs from image")
	}
}

func TestWindowsSplitFrameComposesActualPanePixels(t *testing.T) {
	left := windowTestFrame(t, 2, 2)
	right := windowTestFrame(t, 2, 2)
	frame := windowsWindowFrame{Width: 4, Height: 2, Target: "split-target", Panes: []windowsWindowPane{{windowsWindowFrame: left, X: 0}, {windowsWindowFrame: right, X: 2}}}
	data, err := nativeFramePixels(frame)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data[:4], []byte{30, 110, 240, 0}) || !bytes.Equal(data[8:12], []byte{30, 110, 240, 0}) {
		t.Fatal("pane image was not placed in its real bounds")
	}
	frame.Panes[1].X = 1
	if _, err := nativeFramePixels(frame); err == nil {
		t.Fatal("overlapping panes accepted")
	}
}

func TestWindowsOwnedOverlayClipsAtViewportBounds(t *testing.T) {
	base := windowTestFrame(t, 4, 2)
	overlay := windowTestFrame(t, 2, 2)
	base.Overlays = []windowsWindowPane{{windowsWindowFrame: overlay, X: 3}}
	pixels, err := nativeFramePixels(base)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pixels[12:16], []byte{30, 110, 240, 0}) {
		t.Fatal("native menu did not appear at its real position")
	}
	base.Overlays[0].X = 5
	if _, err := nativeFramePixels(base); err == nil {
		t.Fatal("out-of-bounds overlay accepted")
	}
}
