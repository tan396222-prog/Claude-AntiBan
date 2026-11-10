package workbench

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

type interruptedAttachmentReader struct{}

func (interruptedAttachmentReader) Read(p []byte) (int, error) {
	copy(p, "partial")
	return 7, errors.New("source interrupted")
}

func TestWindowsAttachmentFramesRequireCompleteSource(t *testing.T) {
	for _, input := range []io.Reader{bytes.NewReader([]byte("hello")), bytes.NewReader(nil), interruptedAttachmentReader{}} {
		var wire bytes.Buffer
		err := writeWindowsAttachmentFrames(&wire, input)
		data := wire.Bytes()
		var content bytes.Buffer
		complete := false
		for len(data) >= 4 {
			n := int(binary.LittleEndian.Uint32(data[:4]))
			data = data[4:]
			if n == 0 {
				complete = true
				break
			}
			if n > 64<<10 || n > len(data) {
				t.Fatal("invalid frame")
			}
			content.Write(data[:n])
			data = data[n:]
		}
		if complete != (err == nil) {
			t.Fatal("an interrupted transfer must not contain a completion marker")
		}
	}
}

func TestWindowsAttachmentPathConfinedToGeneratedDirectory(t *testing.T) {
	valid := `C:\Users\CageLabSetup\Cage\Attachments\0123456789abcdef0123456789abcdef\中文 O'Brien.txt`
	if !validWindowsAttachmentPath(valid, "中文 O'Brien.txt") {
		t.Fatal("valid guest path rejected")
	}
	for _, path := range []string{`C:\Users\Cage\secret.txt`, `C:\Users\Cage\Cage\Attachments\..\secret.txt`, `\\server\Cage\Attachments\0123456789abcdef0123456789abcdef\中文 O'Brien.txt`, valid + ":stream", valid + "\n"} {
		if validWindowsAttachmentPath(path, "中文 O'Brien.txt") {
			t.Fatalf("unsafe response accepted: %q", path)
		}
	}
}

func TestWindowsChooserAttachmentPathRetainsNameInPrivateChild(t *testing.T) {
	const batch = "0123456789abcdef0123456789abcdef"
	const file = "fedcba9876543210fedcba9876543210"
	base := `C:\Users\guest\Cage\Attachments\` + batch + `\`
	if !validWindowsAttachmentPath(base+file+`\same.txt`, "same.txt") {
		t.Fatal("fixed chooser file directory rejected")
	}
	for _, path := range []string{
		base + `ordinary\same.txt`,
		base + file + `\` + file + `\same.txt`,
		base + file + `\different.txt`,
		base + `..\same.txt`,
		base + file + `\same.txt:extra`,
	} {
		if validWindowsAttachmentPath(path, "same.txt") {
			t.Fatalf("unconfined chooser attachment accepted: %q", path)
		}
	}
}
