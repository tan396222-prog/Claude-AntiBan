package workbench

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestBrowserCopyContextUsesAuthenticatedGuestRoute(t *testing.T) {
	s := testServer(t)
	s.ready, s.credentials.Token = true, "private-test-token"
	calls := 0
	s.browserControl = browserClientForTest(t, s, browserTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/copy-context" || r.Header.Get("Authorization") != "Bearer private-test-token" {
			t.Fatalf("unexpected copy route: %s %s", r.Method, r.URL.Path)
		}
		var input browserInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if input.ID != "tab" || input.Context != strings.Repeat("a", 32) {
			t.Fatal("copy context or tab was lost")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
	}))
	body := `{"id":"tab","context":"` + strings.Repeat("a", 32) + `"}`
	if response := request(s, "POST", "/api/browser/copy-context", s.origin, body, true); response.Code != http.StatusOK {
		t.Fatalf("copy context: %d %s", response.Code, response.Body.String())
	}
	for _, body := range []string{`{"id":"tab"}`, `{"context":"invalid"}`} {
		if response := request(s, "POST", "/api/browser/copy-context", s.origin, body, true); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid copy context accepted: %d", response.Code)
		}
	}
	if response := request(s, "POST", "/api/browser/copy-context", s.origin, body, false); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated copy context accepted: %d", response.Code)
	}
	if calls != 1 {
		t.Fatalf("rejected request reached guest: %d calls", calls)
	}
}
