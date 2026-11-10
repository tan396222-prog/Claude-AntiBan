package workbench

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sylvinus/agent-vm/internal/guestos"
)

func windowsBrowserRequestForTest(s *Server, method, path string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, s.origin+path, bytes.NewReader(body))
	r.Header.Set("Origin", s.origin)
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: s.cookieName(), Value: s.session})
	r.AddCookie(&http.Cookie{Name: s.cookieName() + "-desktop", Value: trustedViewProof(s.token)})
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, r)
	return w
}

func TestWindowsBrowserImageHTTPUsesBoundedNativePNGChunks(t *testing.T) {
	s := testServer(t)
	s.windowsGuest, s.ready = &guestos.WindowsProfile{}, true
	s.windowsBrowserControl = fixtureWindowsBrowserClient(t)
	img := image.NewRGBA(image.Rect(0, 0, 512, 512))
	var state uint32 = 0x7450133
	for y := 0; y < 512; y++ {
		for x := 0; x < 512; x++ {
			state = state*1664525 + 1013904223
			img.SetRGBA(x, y, color.RGBA{byte(state), byte(state >> 8), byte(state >> 16), 255})
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	if data.Len() < 128<<10 {
		t.Fatal("PNG fixture did not exercise bounded relay chunks")
	}
	body, _ := json.Marshal(map[string]any{"target": "browser", "id": strings.Repeat("a", 32), "png": base64.StdEncoding.EncodeToString(data.Bytes())})
	w := windowsBrowserRequestForTest(s, "POST", "/api/input/image", body)
	if w.Code != 200 {
		t.Fatal("Windows PNG seam failed", w.Code)
	}
	if s.browserControl != nil {
		t.Fatal("PNG used Linux transport")
	}
	bad, _ := json.Marshal(map[string]any{"target": "browser", "id": strings.Repeat("a", 32), "png": "not png"})
	if w := windowsBrowserRequestForTest(s, "POST", "/api/input/image", bad); w.Code == 200 {
		t.Fatal("invalid PNG accepted")
	}
}

func TestNativeBrowserTextHTTPPreservesTwoMiBAndRequiresPreparation(t *testing.T) {
	s := testServer(t)
	s.windowsGuest, s.ready = &guestos.WindowsProfile{}, true
	s.windowsBrowserControl = fixtureWindowsBrowserClient(t)
	id := strings.Repeat("a", 32)
	prepared := windowsBrowserRequestForTest(s, http.MethodPost, "/api/input/paste/start", []byte(`{"target":"browser","id":"`+id+`"}`))
	var binding struct {
		Transfer string `json:"transfer"`
	}
	if prepared.Code != http.StatusOK || json.Unmarshal(prepared.Body.Bytes(), &binding) != nil || !validInputToken(binding.Transfer) {
		t.Fatalf("clipboard preparation failed: %d %s", prepared.Code, prepared.Body.String())
	}
	text := strings.Repeat("中", (2<<20)/3) + "ab"
	if len(text) != 2<<20 {
		t.Fatal("fixture is not at the UTF-8 byte limit")
	}
	body, _ := json.Marshal(map[string]any{"target": "browser", "id": id, "text": text, "transfer": binding.Transfer})
	response := windowsBrowserRequestForTest(s, http.MethodPost, "/api/input/text", body)
	if response.Code != http.StatusOK {
		t.Fatalf("bounded text paste failed: %d %s", response.Code, response.Body.String())
	}
	body, _ = json.Marshal(map[string]any{"target": "browser", "id": id, "text": text + "c", "transfer": binding.Transfer})
	if response := windowsBrowserRequestForTest(s, http.MethodPost, "/api/input/text", body); response.Code != http.StatusBadRequest {
		t.Fatal("oversized text was accepted")
	}
	body, _ = json.Marshal(map[string]any{"target": "browser", "id": id, "text": "missing binding"})
	if response := windowsBrowserRequestForTest(s, http.MethodPost, "/api/input/text", body); response.Code != http.StatusConflict {
		t.Fatal("unprepared text was accepted")
	}
}

func TestNativeBrowserClipboardHTTPReadsBoundPNGAndLargeText(t *testing.T) {
	s := testServer(t)
	s.macosGuest, s.ready = &guestos.MacOSProfile{}, true
	s.windowsBrowserControl = fixtureWindowsBrowserClient(t)
	for _, id := range []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"} {
		input := browserClipboardInput{ID: id, Tab: strings.Repeat("a", 32), Context: strings.Repeat("b", 32), Document: strings.Repeat("c", 32)}
		body, _ := json.Marshal(input)
		response := windowsBrowserRequestForTest(s, http.MethodPost, "/api/browser/clipboard", body)
		var result map[string]string
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil {
			t.Fatalf("clipboard reference failed: %d %s", response.Code, response.Body.String())
		}
		if strings.HasPrefix(id, "11111111") {
			if result["png"] != testPNG(t) {
				t.Fatal("PNG reference changed its bytes")
			}
		} else if result["text"] != strings.Repeat("中", (2<<20)/3)+"ab" {
			t.Fatal("large UTF-8 clipboard text changed")
		}
		input.Context = strings.Repeat("d", 32)
		body, _ = json.Marshal(input)
		if response := windowsBrowserRequestForTest(s, http.MethodPost, "/api/browser/clipboard", body); response.Code == http.StatusOK {
			t.Fatal("superseded copy context was accepted")
		}
		input.Context = ""
		body, _ = json.Marshal(input)
		if response := windowsBrowserRequestForTest(s, http.MethodPost, "/api/browser/clipboard", body); response.Code != http.StatusBadRequest {
			t.Fatal("unbound copy reference was accepted")
		}
	}
	if s.browserControl != nil {
		t.Fatal("private clipboard used Linux transport")
	}
}

func TestWindowsBrowserExtensionHTTPUsesNativeChunkedPreview(t *testing.T) {
	s := testServer(t)
	s.windowsGuest, s.ready = &guestos.WindowsProfile{}, true
	s.windowsBrowserControl = fixtureWindowsBrowserClient(t)
	body := bytes.Repeat([]byte{0xab}, 131089)
	w := windowsBrowserRequestForTest(s, http.MethodPost, "/api/browser/extensions/upload", body)
	var preview struct {
		Session string `json:"session"`
		Size    int    `json:"size"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &preview) != nil || !browserFileID.MatchString(preview.Session) || preview.Size != len(body) {
		t.Fatal("Windows extension upload did not return its preview", w.Code)
	}
	if s.browserControl != nil {
		t.Fatal("Windows extension upload created the Linux browser transport")
	}
}

func TestWindowsVaultStoreAndBrowserRoutesRemainAuthorized(t *testing.T) {
	s := testServer(t)
	s.account = newTestAccount(t)
	s.windowsGuest, s.ready = &guestos.WindowsProfile{}, true
	s.windowsBrowserControl = fixtureWindowsBrowserClient(t)
	save := []byte(`{"action":"save","entry":{"origin":"https://mail.example.com","username":"fixture-user","password":"fixture-only-secret"}}`)
	if w := windowsBrowserRequestForTest(s, "POST", "/api/vault", save); w.Code != 200 {
		t.Fatal("Windows vault save refused", w.Code)
	}
	w := windowsBrowserRequestForTest(s, "GET", "/api/vault", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "fixture-only-secret") {
		t.Fatal("Windows vault list refused or exposed a password", w.Code)
	}
	w = windowsBrowserRequestForTest(s, "POST", "/api/vault/browser", []byte(`{"action":"status","tab":"tab"}`))
	var state vaultBrowserState
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &state) != nil || !state.Available {
		t.Fatal("Windows vault browser seam unavailable", w.Code)
	}
	if s.browserControl != nil {
		t.Fatal("Windows vault created a Linux browser transport")
	}
	if w := request(s, "GET", "/api/vault", s.origin, "", true); w.Code != 403 {
		t.Fatal("Windows vault admitted a viewer without native trust", w.Code)
	}
	s.account.lock()
	if w := windowsBrowserRequestForTest(s, "GET", "/api/vault", nil); w.Code != http.StatusLocked {
		t.Fatal("locked Windows vault admitted", w.Code)
	}
	for _, path := range []string{"/api/vault/export", "/api/vault/browser/extra"} {
		if windowsWorkbenchRoute(path) {
			t.Fatal("unregistered vault route admitted")
		}
	}
	if !windowsWorkbenchRoute("/api/client/browser-extensions") || windowsWorkbenchRoute("/api/client/browser-extensions/extra") {
		t.Fatal("host extension import route boundary changed")
	}
}
