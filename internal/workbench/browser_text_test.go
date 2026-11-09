package workbench

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestBrowserTextRequiresSessionAndExplicitTarget(t *testing.T) {
	s := testServer(t)
	s.ready = true
	s.credentials.Token = "guest-test-token"
	calls := 0
	s.browserControl = browserClientForTest(t, s, browserTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "http://127.0.0.1:18500/insert-text" || r.Header.Get("Authorization") != "Bearer guest-test-token" {
			t.Fatal("wrong text destination or authentication")
		}
		var input struct {
			ID   string
			Text string
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.ID != "tab-1" || input.Text != "是，大奥🙂" {
			t.Fatal("text or target changed", err)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
	}))
	path := "/api/browser/insert-text"
	valid := `{"id":"tab-1","text":"是，大奥🙂"}`
	if w := request(s, "POST", path, s.origin, valid, false); w.Code != 401 {
		t.Fatal("unauthenticated input accepted", w.Code)
	}
	if w := request(s, "POST", path, "https://example.com", valid, true); w.Code != 403 {
		t.Fatal("cross-origin input accepted", w.Code)
	}
	for _, body := range []string{`{"text":"是"}`, `{"id":"tab-1"}`, `{"id":"tab-1","text":""}`, `{"id":"tab-1","text":"` + strings.Repeat("中", 3000) + `"}`} {
		if w := request(s, "POST", path, s.origin, body, true); w.Code != 400 {
			t.Fatal("invalid input accepted", w.Code)
		}
	}
	if calls != 0 {
		t.Fatal("rejected input reached guest")
	}
	if w := request(s, "POST", path, s.origin, valid, true); w.Code != 200 || calls != 1 {
		t.Fatal("valid input rejected", w.Code, w.Body.String())
	}
}

func TestBrowserIMERequiresSessionAndValidSelection(t *testing.T) {
	s := testServer(t)
	s.ready = true
	s.credentials.Token = "guest-test-token"
	var requests []map[string]any
	s.browserControl = browserClientForTest(t, s, browserTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/ime" || r.Header.Get("Authorization") != "Bearer guest-test-token" {
			t.Fatalf("wrong IME destination or authentication: %s", r.URL.Path)
		}
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, input)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
	}))
	path := "/api/input"
	valid := `{"target":"browser","session":"0123456789abcdef0123456789abcdef","id":"tab-1","mode":"update","text":"nihao","selectionStart":5,"selectionEnd":5}`
	if w := request(s, "POST", path, s.origin, valid, false); w.Code != 401 {
		t.Fatal("unauthenticated IME input accepted", w.Code)
	}
	if w := request(s, "POST", path, "https://example.com", valid, true); w.Code != 403 {
		t.Fatal("cross-origin IME input accepted", w.Code)
	}
	for _, body := range []string{
		`{"target":"desktop","id":"tab-1","mode":"status"}`,
		`{"target":"browser","id":"tab-1","mode":"begin","session":"0123456789abcdef0123456789abcdef","context":"expired"}`,
		`{"target":"desktop","id":"desktop","mode":"commit","session":"invalid","text":"？"}`,
		`{"target":"browser","session":"0123456789abcdef0123456789abcdef","mode":"update","text":"n"}`,
		`{"target":"browser","session":"0123456789abcdef0123456789abcdef","id":"tab-1","mode":"unknown"}`,
		`{"target":"browser","session":"0123456789abcdef0123456789abcdef","id":"tab-1","mode":"update","text":"nihao","selectionStart":6,"selectionEnd":6}`,
		`{"target":"browser","session":"0123456789abcdef0123456789abcdef","id":"tab-1","mode":"update","text":"n","selectionStart":1,"selectionEnd":0}`,
		`{"target":"browser","session":"0123456789abcdef0123456789abcdef","id":"tab-1","mode":"update","text":"` + strings.Repeat("n", 8193) + `"}`,
	} {
		if w := request(s, "POST", path, s.origin, body, true); w.Code != 400 {
			t.Fatalf("invalid IME input accepted (%s): %d", body[:min(len(body), 80)], w.Code)
		}
	}
	if len(requests) != 0 {
		t.Fatal("rejected IME input reached guest")
	}
	for _, body := range []string{
		valid,
		`{"target":"browser","session":"0123456789abcdef0123456789abcdef","id":"tab-1","mode":"commit","text":"你好","selectionStart":0,"selectionEnd":0}`,
		`{"target":"browser","session":"0123456789abcdef0123456789abcdef","id":"tab-1","mode":"cancel","text":"","selectionStart":0,"selectionEnd":0}`,
	} {
		if w := request(s, "POST", path, s.origin, body, true); w.Code != 200 {
			t.Fatalf("valid IME input rejected: %d %s", w.Code, w.Body.String())
		}
	}
	if len(requests) != 3 || requests[0]["mode"] != "update" || requests[1]["text"] != "你好" || requests[2]["mode"] != "cancel" {
		t.Fatalf("IME update/commit/cancel order changed: %#v", requests)
	}
	for _, body := range []string{
		`{"target":"desktop","id":"desktop","mode":"status"}`,
		`{"target":"desktop","id":"desktop","mode":"begin","session":"0123456789abcdef0123456789abcdef","context":"abcdef0123456789abcdef0123456789","sequence":0}`,
		`{"target":"desktop","id":"desktop","mode":"update","session":"0123456789abcdef0123456789abcdef","sequence":1,"text":"🙂n","selectionStart":2,"selectionEnd":3}`,
	} {
		if w := request(s, "POST", path, s.origin, body, true); w.Code != 200 {
			t.Fatalf("desktop input rejected: %d %s", w.Code, w.Body.String())
		}
	}
	if requests[3]["target"] != "desktop" || requests[4]["context"] != "abcdef0123456789abcdef0123456789" || requests[4]["sequence"] != float64(0) || requests[5]["sequence"] != float64(1) || requests[5]["selectionEnd"] != float64(3) {
		t.Fatal("Desktop session or UTF-16 selection changed")
	}
}
