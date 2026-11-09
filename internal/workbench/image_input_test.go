package workbench

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"
)

func testPNG(t *testing.T) string {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(data.Bytes())
}

func TestInputImageValidation(t *testing.T) {
	valid := testPNG(t)
	if _, err := decodeInputImage(valid); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"", "!", base64.StdEncoding.EncodeToString([]byte("not PNG")), valid[:len(valid)-16], strings.Repeat("A", base64.StdEncoding.EncodedLen(maxImageBytes)+4)} {
		if _, err := decodeInputImage(input); err == nil {
			t.Fatal("invalid image accepted")
		}
	}
	oversized, _ := base64.StdEncoding.DecodeString(valid)
	binary.BigEndian.PutUint32(oversized[16:20], 40_000_001)
	binary.BigEndian.PutUint32(oversized[29:33], crc32.ChecksumIEEE(oversized[12:29]))
	if _, err := decodeInputImage(base64.StdEncoding.EncodeToString(oversized)); err == nil {
		t.Fatal("oversized dimensions accepted")
	}
}

func TestImageInputAuthenticationAndDestination(t *testing.T) {
	s := testServer(t)
	s.ready = true
	s.credentials.Token = "guest-test-token"
	calls := 0
	s.browserControl = browserClientForTest(t, s, browserTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/paste-image" || r.Header.Get("Authorization") != "Bearer guest-test-token" {
			t.Fatal("wrong destination or credentials")
		}
		var input map[string]string
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input["id"] != "tab-1" || input["png"] != testPNG(t) {
			t.Fatal("image or target changed", err)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
	}))
	body := `{"target":"browser","id":"tab-1","png":"` + testPNG(t) + `"}`
	path := "/api/input/image"
	if w := request(s, "POST", path, s.origin, body, false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := request(s, "POST", path, "https://example.com", body, true); w.Code != 403 {
		t.Fatal(w.Code)
	}
	for _, bad := range []string{`{"target":"browser","png":"` + testPNG(t) + `"}`, `{"target":"host","id":"tab-1"}`, `{"target":"terminal","id":"missing","png":"` + testPNG(t) + `"}`, `{"target":"browser","id":"tab-1","png":"broken"}`} {
		if w := request(s, "POST", path, s.origin, bad, true); w.Code < 400 {
			t.Fatal("invalid image accepted")
		}
	}
	if calls != 0 {
		t.Fatal("invalid input reached guest")
	}
	if w := request(s, "POST", path, s.origin, body, true); w.Code != 200 || calls != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	s.ready = false
	if w := request(s, "POST", path, s.origin, body, true); w.Code != 409 || calls != 1 {
		t.Fatal("offline input accepted")
	}
	s.ready = true
	s.account.lock()
	if w := request(s, "POST", path, s.origin, body, true); w.Code != 423 || calls != 1 {
		t.Fatal("locked account accepted an image")
	}
}
