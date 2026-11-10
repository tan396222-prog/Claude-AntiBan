package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/mobilelink"
)

func pairedHostRequest(s *Server, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, s.origin+path, strings.NewReader(body))
	r.Header.Set("Origin", s.origin)
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: s.cookieName(), Value: s.session})
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, r)
	return w
}

// Called with a real QR enrollment, relay tunnel, TLS peer and authenticated
// workbench. Guest HTTP responses and the X display effect use bounded fixtures.
func testPairedBrowser(t *testing.T, ctx context.Context, s *Server, phone *mobilelink.Client, gate *cage.EgressGate) {
	t.Helper()
	s.phones.mu.Lock()
	profile := s.phones.peerLocked()
	s.phones.mu.Unlock()
	if len(profile) != 64 {
		t.Fatal("missing enrollment profile")
	}
	const customHome = "https://example.com/start?source=desktop"
	const blockedHome = "http://127.0.0.1/private"
	openedURLs := make(chan string, 6)
	const frameTag = "b542cd94286a2489bc2df1241b043c76"
	var opens, desktopFrames atomic.Int32
	var desktopVisible atomic.Bool
	s.desktopDisplayUpdate = func(show bool) error { desktopVisible.Store(show); return nil }
	var slowFrame, desktopDensity atomic.Bool
	var desktopOpenStatus, desktopFrameStatus atomic.Int32
	receiving := make(chan struct{})
	id := uuid.NewString()
	s.browserControl = browserClientForTest(t, s, browserTransport(func(r *http.Request) (*http.Response, error) {
		var input phoneSurfaceInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if input.ID != id {
			t.Error("unexpected guest target")
		}
		body := fmt.Sprintf(`{"id":%q}`, id)
		switch r.URL.Path {
		case "/phone-browser/open":
			openedURLs <- input.URL
			if input.URL == blockedHome {
				return &http.Response{StatusCode: 403, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}"))}, nil
			}
			if input.Profile != profile || input.Width != 390 || input.Height != 640 {
				t.Error("isolated profile or viewport not forwarded")
			}
			opens.Add(1)
		case "/phone-browser/frame":
			if input.After == frameTag {
				body = fmt.Sprintf(`{"id":%q,"frame_tag":%q,"unchanged":true}`, id, frameTag)
				break
			}
			if slowFrame.Load() {
				close(receiving)
				<-r.Context().Done()
				return nil, r.Context().Err()
			}
			body = fmt.Sprintf(`{"id":%q,"document":"test","image":"dGVzdA==","width":390,"height":640}`, id)
		case "/phone-browser/action":
			if input.ReturnFrame {
				body = fmt.Sprintf(`{"id":%q,"frame_tag":%q,"image":%q}`, id, frameTag, strings.Repeat("A", 8192))
			}
			if input.Type != "tap" || input.Document != "test" || input.X == nil || *input.X != 0 {
				t.Error("tap not bound to frame or zero coordinate discarded")
			}
		case "/phone-browser/close":
		case "/phone-desktop/open":
			if !desktopVisible.Load() {
				t.Error("phone opened before its display lease restored the window")
			}
			if status := desktopOpenStatus.Load(); status != 0 {
				return &http.Response{StatusCode: int(status), Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
			}
			if input.Profile != "" || input.Width != 390 || input.URL != "" {
				t.Error("desktop launch scope or geometry lost")
			}
		case "/phone-desktop/frame":
			if status := desktopFrameStatus.Load(); status != 0 {
				return &http.Response{StatusCode: int(status), Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
			}
			if desktopFrames.Add(1) == 1 {
				return &http.Response{StatusCode: 409, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
			}
			body = fmt.Sprintf(`{"id":%q,"document":"test","window":"desktop","image":"ZGVza3RvcA==","width":390,"height":640}`, id)
			if desktopDensity.Load() {
				body = strings.TrimSuffix(body, "}") + `,"pixel_ratio_control":true,"pixel_ratio":3.5,"requested_pixel_ratio":3.5}`
			}
		case "/phone-desktop/action":
			if input.Type == "resize" && input.PixelRatio == 3.5 && input.Width == 390 && input.Height == 640 {
				break
			}
			if input.Type != "login" || input.Document != "test" {
				t.Error("wrong desktop window selection")
			}
		case "/phone-desktop/close":
		default:
			t.Errorf("unexpected guest route %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	}))
	operation := func(surface, method, path, body string, want int) string {
		t.Helper()
		reply, err := phone.Do(ctx, method, "/api/mobile/"+surface+"/"+path, []byte(body))
		if err != nil {
			t.Fatal(err)
		}
		if reply.Status != want {
			t.Fatalf("phone %s: got HTTP %d, want %d (%s)", path, reply.Status, want, reply.Body)
		}
		return string(reply.Body)
	}
	do := func(method, path, body string, want int) string {
		t.Helper()
		return operation("browser", method, path, body, want)
	}
	screen := func(method, path, body string, want int) string {
		t.Helper()
		return operation("desktop", method, path, body, want)
	}
	open := fmt.Sprintf(`{"id":%q,"width":390,"height":640}`, id)
	if pairedHostRequest(s, "POST", "/api/mobile/desktop/open", open).Code != 403 {
		t.Fatal("unscoped transport could access desktop")
	}
	screen("POST", "open", strings.Replace(open, `"width"`, `"url":"https://example.test","width"`, 1), 400)
	screen("POST", "open", open, 200)
	screen("GET", id+"/frame", "", 425)
	if !strings.Contains(screen("GET", id+"/frame", "", 200), "ZGVza3RvcA==") {
		t.Fatal("wrong guest display")
	}
	// A phone waits for the normal desktop startup job, then attaches with
	// its original viewport and no separate account profile.
	s.phones.desktop.op.Lock()
	s.phones.desktop.session.startJob = "shared-desktop-start"
	s.phones.desktop.session.openInput = phoneSurfaceInput{ID: id, Width: 390, Height: 640}
	s.phones.desktop.session.readyUntil = time.Now().Add(time.Minute)
	s.phones.desktop.op.Unlock()
	s.setJobForTest(Job{ID: "shared-desktop-start", Running: true})
	screen("GET", id+"/frame", "", 425)
	s.setJobForTest(Job{ID: "shared-desktop-start"})
	// The service job can finish before its phone socket accepts connections.
	desktopOpenStatus.Store(412)
	screen("GET", id+"/frame", "", 425)
	desktopOpenStatus.Store(503)
	screen("GET", id+"/frame", "", 425)
	desktopOpenStatus.Store(0)
	desktopFrameStatus.Store(503)
	screen("GET", id+"/frame", "", 425)
	desktopFrameStatus.Store(0)
	screen("GET", id+"/frame", "", 200)
	// Once a frame is delivered, genuine service failures remain visible.
	desktopFrameStatus.Store(503)
	screen("GET", id+"/frame", "", 502)
	desktopFrameStatus.Store(0)
	screen("POST", id+"/action", `{"type":"navigate","url":"https://example.test"}`, 400)
	screen("POST", id+"/action", `{"type":"login","document":"test"}`, 200)
	desktopDensity.Store(true)
	if !strings.Contains(screen("GET", id+"/frame", "", 200), `"pixel_ratio_supported":true`) {
		t.Fatal("host did not negotiate pixel density with a capable guest")
	}
	screen("POST", id+"/action", `{"type":"resize","width":390,"height":640,"pixel_ratio":3.5}`, 200)
	screen("POST", id+"/action", `{"type":"resize","width":390,"height":640,"pixel_ratio":-1}`, 400)
	screen("POST", id+"/action", `{"type":"resize","width":390,"height":640,"pixel_ratio":9}`, 400)
	if pairedHostRequest(s, "POST", "/api/mobile/browser/open", open).Code != 403 {
		t.Fatal("CLI-only/unscoped transport used paired device permission")
	}
	do("POST", "open", strings.Replace(open, `"width"`, `"profile":"override","width"`, 1), 400)
	s.mu.Lock()
	s.preferences.BrowserHomeURL = customHome
	s.mu.Unlock()
	if reply := do("POST", "open", open, 200); !strings.Contains(reply, `"action_frame":true`) {
		t.Fatal("browser open did not advertise action frames")
	}
	if url := <-openedURLs; url != customHome {
		t.Fatalf("phone opened %q instead of desktop home", url)
	}
	do("POST", "open", open, 200)
	if opens.Load() != 1 {
		t.Fatal("duplicate open started a second guest browser")
	}
	if !strings.Contains(do("GET", id+"/frame", "", 200), "dGVzdA==") {
		t.Fatal("frame did not cross paired link")
	}
	do("POST", id+"/action", `{"type":"tap","x":0,"y":32,"document":"test"}`, 200)
	if out := do("GET", id+"/frame?after="+frameTag, "", 200); !strings.Contains(out, `"unchanged":true`) || strings.Contains(out, `"image"`) {
		t.Fatal("unchanged frame did not round-trip through the paired transport")
	}
	if out := do("POST", id+"/action", `{"type":"tap","x":0,"y":32,"document":"test","return_frame":true}`, 200); !strings.Contains(out, strings.Repeat("A", 8192)) {
		t.Fatal("attached action frame truncated or omitted")
	}
	do("POST", id+"/action", `{"type":"cdp"}`, 400)
	do("GET", uuid.NewString()+"/frame", "", 410)
	do("POST", id+"/close", "{}", 200)
	do("POST", "open", strings.Replace(open, `"width"`, `"url":"https://claude.ai/","width"`, 1), 200)
	if url := <-openedURLs; url != "https://claude.ai/" {
		t.Fatal("explicit shortcut overridden by home")
	}
	do("POST", id+"/close", "{}", 200)
	s.mu.Lock()
	s.preferences.BrowserHomeURL = blockedHome
	s.mu.Unlock()
	if reply := do("POST", "open", open, 200); !strings.Contains(reply, "home_notice") {
		t.Fatal("missing message for unavailable homepage")
	}
	if first, next := <-openedURLs, <-openedURLs; first != blockedHome || next != "" {
		t.Fatal("unavailable homepage must leave a usable browser")
	}
	// Both surfaces can use the same UUID. Closing one must leave the other usable.
	screen("POST", id+"/close", "{}", 200)
	do("GET", id+"/frame", "", 200)
	desktopOpenStatus.Store(503)
	screen("POST", "open", open, 200)
	screen("GET", id+"/frame", "", 425)
	s.phones.desktop.op.Lock()
	s.phones.desktop.session.readyUntil = time.Now().Add(-time.Second)
	s.phones.desktop.op.Unlock()
	// Startup failures stop waiting after a bounded interval.
	screen("GET", id+"/frame", "", 502)
	desktopOpenStatus.Store(0)
	screen("POST", "open", open, 200)
	// Closing the network gate blocks the next request. Locking the account
	// must also cancel a capture already waiting on the guest.
	slowFrame.Store(true)
	pending := make(chan int, 1)
	go func() {
		reply, err := phone.Do(ctx, "GET", "/api/mobile/browser/"+id+"/frame", nil)
		if err != nil {
			pending <- 0
			return
		}
		pending <- reply.Status
	}()
	select {
	case <-receiving:
	case <-ctx.Done():
		t.Fatal("guest never received frame request")
	}
	gate.Close("network revoked")
	screen("GET", id+"/frame", "", 409)
	s.account.lock()
	select {
	case code := <-pending:
		if code == 200 {
			t.Fatal("frame disclosed after account lock")
		}
	case <-ctx.Done():
		t.Fatal("capture survived account lock")
	}
}
