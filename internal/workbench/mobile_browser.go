package workbench

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/mobilelink"
)

type phoneSurface string

const (
	phoneBrowser phoneSurface = "browser"
	phoneDesktop phoneSurface = "desktop"
)

func (surface phoneSurface) label() string {
	if surface == phoneDesktop {
		return "Claude Desktop"
	}
	return "远程浏览器"
}

type phoneSurfaceState struct {
	op            sync.Mutex
	session       *phoneSurfaceSession        // guarded by op
	nativeBrowser *windowsBrowserClient       // Native guest phone browser, guarded by op
	nativeDesktop *windowsPhoneDesktopSession // Windows shared app window, guarded by op
}

func (p *mobilePairing) surface(kind phoneSurface) *phoneSurfaceState {
	if kind == phoneDesktop {
		return &p.desktop
	}
	return &p.browser
}

func (p *mobilePairing) pairedRequestLocked(r *http.Request) bool {
	return p.roomCtx != nil && p.roomCtx.Err() == nil && p.roomProof != "" && equalSecret(p.roomProof, r.Header.Get(mobilelink.PhoneRoomHeader))
}

func (s *Server) mobileSurfaceRoutes(mux routeRegistrar) {
	mux.HandleFunc("GET /api/mobile/browser/favorites", s.serveFavorites)
	mux.HandleFunc("POST /api/mobile/browser/favorites", s.serveFavorites)
	for _, surface := range []phoneSurface{phoneBrowser, phoneDesktop} {
		route := "/api/mobile/" + string(surface)
		handle := func(w http.ResponseWriter, r *http.Request) { s.phoneSurfaceRequest(surface, w, r) }
		mux.HandleFunc("POST "+route+"/open", handle)
		mux.HandleFunc("GET "+route+"/{id}/frame", handle)
		mux.HandleFunc("POST "+route+"/{id}/action", handle)
		mux.HandleFunc("POST "+route+"/{id}/close", handle)
	}
}

// Keep the private guest profile stable across reconnects to the same enrollment.
func (p *mobilePairing) peerLocked() string {
	if p.record == nil {
		return ""
	}
	digest := sha256.Sum256(p.record.Seed)
	return hex.EncodeToString(digest[:])
}

type phoneSurfaceSession struct {
	releaseDisplay func()
	id, token      string
	ctx            context.Context
	cancel         context.CancelFunc
	stopEpoch      func() bool
	startJob       string
	openInput      phoneSurfaceInput
	readyUntil     time.Time
}

type phoneSurfaceInput struct {
	After       string   `json:"after,omitempty"`
	ReturnFrame bool     `json:"return_frame,omitempty"`
	ID          string   `json:"id,omitempty"`
	Profile     string   `json:"profile,omitempty"`
	Width       int      `json:"width,omitempty"`
	Height      int      `json:"height,omitempty"`
	PixelRatio  float64  `json:"pixel_ratio,omitempty"`
	URL         string   `json:"url,omitempty"`
	Type        string   `json:"type,omitempty"`
	X           *float64 `json:"x,omitempty"`
	Y           *float64 `json:"y,omitempty"`
	DX          *float64 `json:"dx,omitempty"`
	DY          *float64 `json:"dy,omitempty"`
	Document    string   `json:"document,omitempty"`
	Focus       string   `json:"focus,omitempty"`
	Text        string   `json:"text,omitempty"`
	Key         string   `json:"key,omitempty"`
}

func (in phoneSurfaceInput) valid(surface phoneSurface, op string) bool {
	if in.PixelRatio != 0 && (surface != phoneDesktop || in.PixelRatio < 1 || in.PixelRatio > 8 ||
		(op != "open" && !(op == "action" && in.Type == "resize"))) {
		return false
	}
	sizeOK := in.Width >= 240 && in.Width <= 1440 && in.Height >= 160 && in.Height <= 1600
	urlOK := func() bool {
		addr, err := url.Parse(in.URL)
		return err == nil && len(in.URL) <= 16384 && (addr.Scheme == "https" || addr.Scheme == "http") && addr.Hostname() != "" && addr.User == nil
	}
	switch op {
	case "open":
		return sizeOK && (in.URL == "" || surface == phoneBrowser && urlOK())
	case "frame", "close":
		return true
	case "action":
		if len(in.Document) > 128 || len(in.Focus) > 128 {
			return false
		}
		switch in.Type {
		case "navigate":
			return surface == phoneBrowser && urlOK()
		case "resize":
			return sizeOK
		}
		if in.Document == "" {
			return false
		}
		switch in.Type {
		case "tap", "scroll":
			return in.X != nil && in.Y != nil
		case "text":
			return in.Focus != "" && in.Text != "" && len(in.Text) <= 8192
		case "key":
			if surface == phoneDesktop && len(in.Key) == 1 && in.Key[0] >= 0x20 && in.Key[0] <= 0x7e {
				return in.Focus != ""
			}
			switch in.Key {
			case "Enter":
				return in.Focus != ""
			case "Backspace", "Tab", "Escape", "ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "Delete":
				return true
			}
		case "back", "forward", "reload":
			return surface == phoneBrowser
		case "desktop", "login":
			return surface == phoneDesktop
		}
	}
	return false
}

func (s *Server) phoneSurfaceNetwork(ctx context.Context) bool {
	s.mu.Lock()
	blocked := !s.ready || s.closing || s.networkPaused || s.proxyNetworkLost
	s.mu.Unlock()
	if blocked {
		return false
	}
	cfg, err := loadConfig(s.configPath)
	if err != nil {
		return false
	}
	client, err := s.gateClient()
	if err != nil {
		return false
	}
	gate, err := client.Status(ctx)
	now := time.Now()
	return err == nil && gate.Configuration == cfg.Fingerprint() && gate.Mode == cage.GateOpen && gate.PolicyVerified &&
		!gate.Fault && gate.Until.After(now) && gate.Report != nil && gate.Report.Authorizes(gate.RunID, gate.Configuration, now) == nil
}

// Caller holds this surface’s operation lock. Closing only this ID lets later opens safely use a
// fresh session after a lock, disconnected phone, failed open or revocation.
func (s *Server) retirePhoneSurface(surface phoneSurface, b *phoneSurfaceSession) {
	state := s.phones.surface(surface)
	if b == nil || state.session != b {
		return
	}
	state.session = nil
	b.stopEpoch()
	b.cancel()
	input := phoneSurfaceInput{ID: b.id}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _, _ = s.phoneSurfaceGuest(ctx, surface, b.token, "close", input)
	if b.releaseDisplay != nil {
		b.releaseDisplay()
		b.releaseDisplay = nil
	}
	if surface == phoneBrowser && state.nativeBrowser != nil {
		state.nativeBrowser.close()
		state.nativeBrowser = nil
	}
}

func (s *Server) phoneSurfaceGuest(ctx context.Context, surface phoneSurface, token, op string, input phoneSurfaceInput) ([]byte, int, error) {
	if surface == phoneDesktop && s.windowsGuest != nil {
		return s.windowsPhoneDesktop(ctx, token, op, input)
	}
	if surface == phoneDesktop && s.macosGuest != nil {
		return s.macOSPhoneDesktop(ctx, token, op, input)
	}
	if surface == phoneBrowser && (s.windowsGuest != nil || s.macosGuest != nil) {
		client := s.phones.browser.nativeBrowser
		if client == nil {
			return nil, 410, nil
		}
		out, err := client.request(ctx, "POST", "/phone-browser/"+op, input)
		if err != nil {
			var rejected windowsPhoneBrowserError
			if errors.As(err, &rejected) {
				return nil, rejected.status, nil
			}
			return nil, 502, err
		}
		limit := 4096
		if op == "frame" || input.ReturnFrame {
			limit = 3 << 20
		}
		if len(out) > limit || !json.Valid(out) {
			return nil, 502, nil
		}
		return out, 200, nil
	}
	data, err := json.Marshal(input)
	if err != nil {
		return nil, 400, err
	}
	response, err := s.browserClient().open(ctx, token, "POST", "/phone-"+string(surface)+"/"+op, "application/json", bytes.NewReader(data), int64(len(data)), 15*time.Second)
	if err != nil {
		return nil, 502, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, response.StatusCode, nil
	}
	limit := int64(4096)
	if op == "frame" || input.ReturnFrame {
		limit = 3 << 20
	}
	out, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, 502, err
	}
	if int64(len(out)) > limit || !json.Valid(out) {
		return nil, 502, nil
	}
	return out, 200, nil
}

func (s *Server) phoneSurfaceRequest(surface phoneSurface, w http.ResponseWriter, r *http.Request) {
	p := &s.phones
	p.mu.Lock()
	state := p.surface(surface)
	room, profile := p.roomCtx, p.peerLocked()
	allowed := p.pairedRequestLocked(r)
	p.mu.Unlock()
	if !allowed {
		http.Error(w, "请通过电脑 Cage 的二维码或配对码连接这台主机，再打开"+surface.label()+"。", 403)
		return
	}
	epoch, _ := r.Context().Value(mobileEpochKey{}).(context.Context)
	if epoch == nil || epoch.Err() != nil {
		http.Error(w, "请先解锁 Cage", 423)
		return
	}
	op := r.URL.Path[strings.LastIndexByte(r.URL.Path, '/')+1:]
	var input phoneSurfaceInput
	if r.Method == http.MethodPost && !readJSON(w, r, &input, 48<<10) {
		return
	}
	if input.After != "" || input.Profile != "" || (op != "open" && input.ID != "") ||
		(input.ReturnFrame && (surface != phoneBrowser || op != "action")) {
		http.Error(w, "图形会话请求无效", 400)
		return
	}
	if r.URL.RawQuery != "" {
		after, ok := mobilelink.FrameAfter(r.URL.RawQuery)
		if !ok || surface != phoneBrowser || op != "frame" {
			http.Error(w, "画面请求无效", 400)
			return
		}
		input.After = after
	}
	if op != "open" {
		input.ID = r.PathValue("id")
	}
	if !mobileID(input.ID) {
		http.Error(w, "手机会话编号无效", 400)
		return
	}
	if !input.valid(surface, op) {
		http.Error(w, "操作参数或画面尺寸无效", 400)
		return
	}
	defaultHome := op == "open" && surface == phoneBrowser && input.URL == ""
	if op == "open" && surface == phoneBrowser {
		input.Profile = profile
		if defaultHome {
			s.mu.Lock()
			input.URL = browserHomeURL(s.preferences)
			s.mu.Unlock()
		}
	}
	// Serialize frames, geometry and user actions on one phone, without
	// blocking account locking, link revocation or desktop window operations.
	state.op.Lock()
	defer state.op.Unlock()
	if r.Context().Err() != nil || room.Err() != nil || epoch.Err() != nil {
		http.Error(w, surface.label()+"授权已结束", 403)
		return
	}
	b := state.session
	if b != nil && (b.ctx.Err() != nil || op == "open" && b.id != input.ID) {
		s.retirePhoneSurface(surface, b)
		b = nil
	}
	if op != "open" && (b == nil || b.id != input.ID) {
		http.Error(w, surface.label()+"会话已结束，请重新打开", 410)
		return
	}
	if op == "close" {
		s.retirePhoneSurface(surface, b)
		jsonReply(w, map[string]string{"id": input.ID})
		return
	}
	if !s.phoneSurfaceNetwork(r.Context()) {
		s.retirePhoneSurface(surface, b)
		http.Error(w, "请在电脑启动 Cage，并确保网络检查已放行。", 409)
		return
	}
	if op == "open" && b != nil {
		jsonReply(w, map[string]any{"id": input.ID, "action_frame": surface == phoneBrowser})
		return
	}
	if b == nil {
		sessionCtx, cancel := context.WithCancel(room)
		s.mu.Lock()
		token := s.credentials.Token
		s.mu.Unlock()
		if surface == phoneDesktop && (s.macosGuest != nil || s.windowsGuest != nil) {
			token = randomToken()
		}
		b = &phoneSurfaceSession{id: input.ID, token: token, ctx: sessionCtx, cancel: cancel, stopEpoch: context.AfterFunc(epoch, cancel)}
		if surface == phoneBrowser && s.windowsGuest != nil {
			state.nativeBrowser = newWindowsBrowserClient(s.lifetimeContext(), s.profile)
		}
		if surface == phoneBrowser && s.macosGuest != nil {
			state.nativeBrowser = s.macOSPhoneBrowserClient(s.lifetimeContext())
		}
		if surface == phoneDesktop {
			b.readyUntil = time.Now().Add(90 * time.Second)
		}
		state.session = b
		if surface == phoneDesktop {
			release, err := s.acquireDesktopView()
			if err != nil {
				s.retirePhoneSurface(surface, b)
				http.Error(w, "无法恢复 Claude Desktop 画面，请重新打开", http.StatusBadGateway)
				return
			}
			b.releaseDisplay = release
		}
		context.AfterFunc(sessionCtx, func() { state.op.Lock(); defer state.op.Unlock(); s.retirePhoneSurface(surface, b) })
	}
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(b.ctx, cancel)
	defer func() { stop(); cancel() }()
	if b.startJob != "" || b.openInput.ID != "" {
		job := s.jobs.snapshot()
		if b.startJob != "" && job.ID == b.startJob && !job.Running && job.Error != "" {
			s.retirePhoneSurface(surface, b)
			http.Error(w, "电脑上的 Claude Desktop 未能启动，请在电脑上查看操作详情", 502)
			return
		}
		if b.startJob != "" && job.ID == b.startJob && job.Running && time.Now().Before(b.readyUntil) {
			http.Error(w, "正在打开电脑上的 Claude Desktop", http.StatusTooEarly)
			return
		}
		_, status, err := s.phoneSurfaceGuest(ctx, surface, b.token, "open", b.openInput)
		if err != nil || status != 200 {
			if err == nil && desktopPreparing(status) && time.Now().Before(b.readyUntil) {
				http.Error(w, "正在打开 Claude Desktop", http.StatusTooEarly)
				return
			}
			s.retirePhoneSurface(surface, b)
			http.Error(w, "无法连接电脑上的 Claude Desktop，请重新打开", 502)
			return
		}
		b.startJob, b.openInput = "", phoneSurfaceInput{}
	}
	out, status, err := s.phoneSurfaceGuest(ctx, surface, b.token, op, input)
	homeUnavailable := defaultHome && err == nil && (status == http.StatusBadRequest || status == http.StatusForbidden)
	if homeUnavailable && ctx.Err() == nil {
		// A desktop homepage may be private. A blank session keeps the
		// address bar and favorites usable under the phone's public-web policy.
		input.URL = ""
		out, status, err = s.phoneSurfaceGuest(ctx, surface, b.token, op, input)
	}
	if b.ctx.Err() != nil || epoch.Err() != nil || room.Err() != nil {
		s.retirePhoneSurface(surface, b)
		http.Error(w, surface.label()+"授权已结束", 410)
		return
	}
	if r.Context().Err() != nil {
		if op == "open" {
			s.retirePhoneSurface(surface, b)
		}
		return
	}
	if surface == phoneDesktop && op == "open" && err == nil && status == http.StatusPreconditionFailed {
		job := s.jobs.snapshot()
		var err error
		if !job.Running || job.Kind != "claude-desktop-start" {
			job, err = s.submitJob("claude-desktop-start", s.startClaudeDesktop)
		}
		if err != nil {
			s.retirePhoneSurface(surface, b)
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		b.startJob, b.openInput = job.ID, input
		jsonReply(w, map[string]string{"id": input.ID})
		return
	}
	if surface == phoneDesktop && err == nil && time.Now().Before(b.readyUntil) && desktopPreparing(status) {
		if op == "open" {
			b.openInput = input
			jsonReply(w, map[string]string{"id": input.ID})
			return
		}
		if op == "frame" {
			http.Error(w, "正在等待 Claude Desktop 窗口", http.StatusTooEarly)
			return
		}
	}
	if (op == "frame" || input.ReturnFrame) && status == 200 && !s.phoneSurfaceNetwork(ctx) {
		s.retirePhoneSurface(surface, b)
		http.Error(w, "电脑上的 Cage 网络检查已暂停", 409)
		return
	}
	if err != nil || status != 200 {
		if op == "open" || status == 410 || status == 404 {
			s.retirePhoneSurface(surface, b)
		}
		if surface == phoneBrowser && status == http.StatusTooEarly {
			http.Error(w, "正在启动客机浏览器", status)
			return
		}
		if surface == phoneBrowser && (op == "open" || op == "frame") && status == http.StatusServiceUnavailable {
			s.retirePhoneSurface(surface, b)
			http.Error(w, "客机浏览器暂时无法启动，请稍后重新打开；若仍失败，请在电脑 Cage 中检查并更新客机组件。", status)
			return
		}
		switch status {
		case 400:
			s.mu.Lock()
			needsUpdate := s.guestUpdateRequired
			s.mu.Unlock()
			if op == "open" && needsUpdate {
				http.Error(w, "请在电脑上更新 Cage 环境后再打开"+surface.label(), 409)
			} else {
				http.Error(w, surface.label()+"操作无效，请检查输入后再试", 400)
			}
		case 409:
			if surface == phoneDesktop && op == "frame" {
				http.Error(w, "正在等待 Claude Desktop 窗口", http.StatusTooEarly)
				return
			}
			http.Error(w, "画面或焦点已变化，请查看最新画面后再操作", 409)
		case 410:
			var native windowsWindowFailure
			if surface == phoneDesktop && errors.As(err, &native) && native.code == "requires_gui_login" {
				http.Error(w, "请在电脑的 Cage 中解锁已绑定的 Windows 客机账户，再从手机重新打开 Claude Desktop。", 410)
			} else {
				http.Error(w, surface.label()+"会话已结束，请重新打开", 410)
			}
		case 404:
			http.Error(w, "当前客机未提供"+surface.label()+"，请在电脑上更新环境", 409)
		case 403:
			if surface == phoneDesktop && s.macosGuest != nil {
				http.Error(w, "请在电脑的 Cage 中完成虚拟 Mac 的屏幕录制和辅助功能授权，再重新打开。", 403)
			} else {
				http.Error(w, "客机拒绝此次访问", 403)
			}
		case 426:
			http.Error(w, "请在电脑的 Cage 中更新 Windows 客机组件，再从手机打开 Claude Desktop。", http.StatusUpgradeRequired)
		case 501:
			if surface == phoneDesktop {
				http.Error(w, "请先在电脑的 Cage 中安装 Claude Desktop，再从手机打开", 501)
			} else {
				http.Error(w, "请在电脑上更新 Cage 环境", 501)
			}
		default:
			http.Error(w, surface.label()+"暂时不可用，请检查电脑上的环境", 502)
		}
		return
	}
	if ctx.Err() != nil || epoch.Err() != nil || room.Err() != nil {
		http.Error(w, surface.label()+"授权已结束", 410)
		return
	}
	if surface == phoneDesktop && op == "frame" {
		b.readyUntil = time.Time{}
	}
	if homeUnavailable {
		jsonReply(w, map[string]any{"id": input.ID, "action_frame": true, "home_notice": "电脑的新标签页暂时无法在手机上打开。可以输入公网网址或打开收藏夹。"})
		return
	}
	// The phone negotiates the fast path with this host. Annotate the guest
	// object without decoding and re-encoding a potentially multi-MB JPEG.
	if surface == phoneBrowser && (op == "open" || op == "frame") {
		out = bytes.TrimSpace(out)
		if len(out) < 2 || out[0] != '{' {
			http.Error(w, "客机返回的浏览器状态无效", 502)
			return
		}
		out = out[:len(out)-1]
		if len(bytes.TrimSpace(out[1:])) != 0 {
			out = append(out, ',')
		}
		out = append(out, `"action_frame":true}`...)
	}
	defer interruptHTTPOnCancel(w, r.WithContext(ctx))()
	if surface == phoneDesktop && op == "frame" {
		var frame map[string]json.RawMessage
		if json.Unmarshal(out, &frame) == nil && string(frame["pixel_ratio_control"]) == "true" {
			// Both this host and the guest understand pixel_ratio. Older hosts
			// must keep receiving the original request shape from new clients.
			frame["pixel_ratio_supported"] = json.RawMessage("true")
			out, _ = json.Marshal(frame)
		}
	}
	if (op == "frame" || input.ReturnFrame) && len(out) > 3<<20 {
		http.Error(w, "客机画面超过传输大小限制，请重新读取", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}

func desktopPreparing(status int) bool {
	return status == http.StatusConflict || status == http.StatusPreconditionFailed || status == http.StatusServiceUnavailable
}
