package workbench

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	qrcode "github.com/skip2/go-qrcode"
	"github.com/sylvinus/agent-vm/internal/diagnostics"
	"github.com/sylvinus/agent-vm/internal/keepawake"
	"github.com/sylvinus/agent-vm/internal/mobilelink"
	"github.com/sylvinus/agent-vm/internal/remotelink"
)

// One UI-managed phone per host. CLI-managed devices keep their separate identities.
type phoneRecord struct {
	Seed                      []byte
	Pin, Relay, Lookup, Admin string
	Device                    *mobilelink.DeviceInfo `json:"device,omitempty"`
}
type mobilePairing struct {
	mu               sync.Mutex
	browser, desktop phoneSurfaceState
	roomProof        string
	roomCtx          context.Context
	pending          context.CancelFunc
	serving          context.CancelFunc
	record           *phoneRecord
	status           string
	closed           bool
	powerCtx         context.Context
	powerDone        <-chan struct{}
	power            keepawake.State
}

func (s *Server) phoneFile() string { return filepath.Join(s.profile, "mobile-paired-device.json") }

// The transport proof grants only phone operations and is revoked with the
// active room. It never grants native desktop administration to a session cookie.
func (s *Server) nativePhoneRoom(r *http.Request) context.Context {
	if r.URL.Path != "/api/mobile/device" && !strings.HasPrefix(r.URL.Path, "/api/mobile/cli/") && !strings.HasPrefix(r.URL.Path, "/api/mobile/browser/") && !strings.HasPrefix(r.URL.Path, "/api/mobile/desktop/") {
		return nil
	}
	s.phones.mu.Lock()
	defer s.phones.mu.Unlock()
	if s.phones.closed || s.phones.record == nil || !s.phones.pairedRequestLocked(r) {
		return nil
	}
	return s.phones.roomCtx
}

func (s *Server) restorePhone() error {
	b, err := os.ReadFile(s.phoneFile())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var r phoneRecord
	if json.Unmarshal(b, &r) != nil || len(r.Seed) != ed25519.SeedSize || !mobilelink.ValidRelayOrigin(r.Relay) {
		return errors.New("无法读取手机配对记录")
	}
	s.phones.mu.Lock()
	defer s.phones.mu.Unlock()
	s.attachPhone(r)
	return nil
}
func (s *Server) attachPhone(r phoneRecord) {
	ctx, cancel := context.WithCancel(context.Background())
	s.phones.serving = cancel
	s.phones.record = &r
	s.phones.powerCtx = ctx
	done := make(chan struct{})
	s.phones.powerDone = done
	go func() {
		defer close(done)
		keepawake.Run(ctx, func(state keepawake.State) {
			s.phones.mu.Lock()
			if s.phones.powerCtx == ctx {
				s.phones.power = state
			}
			s.phones.mu.Unlock()
			if state.Error != "" {
				diagnostics.Log("warn", "mobile-remote", "keep-awake request failed: "+state.Error)
			}
		})
	}()
	go func() {
		delay := time.Second
		for ctx.Err() == nil {
			start := time.Now()
			err := s.phoneRoom(ctx, r)
			if ctx.Err() != nil {
				return
			}
			if err == nil || time.Since(start) > time.Minute {
				delay = time.Second
			} else {
				delay = min(30*time.Second, 2*delay)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
	}()
}
func (s *Server) phoneRoom(ctx context.Context, r phoneRecord) error {
	id, err := remotelink.IdentityFromKey(ed25519.NewKeyFromSeed(r.Seed))
	if err != nil {
		return err
	}
	b, err := mobilelink.OpenWorkbench(ctx, s.origin, s.issueTicket())
	if err != nil {
		return err
	}
	defer b.Close()
	room, err := mobilelink.CreateDiscoverableRoom(ctx, r.Relay, r.Admin, r.Lookup)
	if err != nil {
		return err
	}
	proof := randomToken()
	p := &s.phones
	p.mu.Lock()
	if ctx.Err() != nil || p.closed || p.record == nil || p.record.Lookup != r.Lookup {
		p.mu.Unlock()
		return context.Canceled
	}
	roomCtx, cancelRoom := context.WithCancel(ctx)
	p.roomProof = proof
	p.roomCtx = roomCtx
	p.mu.Unlock()
	defer func() {
		cancelRoom()
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.roomProof == proof {
			p.roomProof = ""
			p.roomCtx = nil
		}
	}()
	b.SetPhoneRoom(proof)
	return mobilelink.ServeHost(ctx, r.Relay, room.ID, room.HostToken, id, r.Pin, b)
}
func (s *Server) stopPhone() {
	p := &s.phones
	p.mu.Lock()
	p.closed = true
	p.roomCtx = nil
	p.roomProof = ""
	if p.pending != nil {
		p.pending()
	}
	if p.serving != nil {
		p.serving()
	}
	done := p.powerDone
	p.powerCtx = nil
	p.power = keepawake.State{}
	p.mu.Unlock()
	if done != nil {
		<-done
	}
}
func (s *Server) phoneStatus(w http.ResponseWriter, r *http.Request) {
	p := &s.phones
	p.mu.Lock()
	defer p.mu.Unlock()
	var device *mobilelink.DeviceInfo
	if p.record != nil {
		device = p.record.Device
	}
	jsonReply(w, map[string]any{"paired": p.record != nil, "status": p.status, "device": device, "keep_awake": p.power})
}

// Only the active, pinned phone tunnel may describe the enrolled phone.
func (s *Server) reportPhoneDevice(w http.ResponseWriter, r *http.Request) {
	var input mobilelink.DeviceInfo
	if !readJSON(w, r, &input, 1024) {
		return
	}
	input.Model = strings.TrimSpace(input.Model)
	if (input.Platform != "android" && input.Platform != "ios") || input.Model == "" || len(input.Model) > 160 || !utf8.ValidString(input.Model) || strings.ContainsFunc(input.Model, unicode.IsControl) {
		http.Error(w, "手机型号无效", http.StatusBadRequest)
		return
	}
	s.account.mu.Lock()
	defer s.account.mu.Unlock()
	if !s.mobileAuthorized(r.Context()) {
		http.Error(w, "请先解锁 Cage", http.StatusLocked)
		return
	}
	p := &s.phones
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.record == nil || !p.pairedRequestLocked(r) {
		http.Error(w, "请通过已配对的手机连接", http.StatusForbidden)
		return
	}
	if p.record.Device == nil || *p.record.Device != input {
		record := *p.record
		record.Device = &input
		if err := writePrivate(s.phoneFile(), record); err != nil {
			requestError(w, err)
			return
		}
		p.record = &record
	}
	jsonReply(w, map[string]bool{"ok": true})
}
func (s *Server) removePhone(w http.ResponseWriter, r *http.Request) {
	p := &s.phones
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending != nil {
		p.pending()
		p.pending = nil
	}
	if err := os.Remove(s.phoneFile()); err != nil && !os.IsNotExist(err) {
		requestError(w, err)
		return
	}
	if p.serving != nil {
		p.serving()
		p.serving = nil
	}
	p.record = nil
	p.powerCtx = nil
	p.power = keepawake.State{}
	p.roomProof = ""
	p.roomCtx = nil
	p.status = ""
	jsonReply(w, map[string]bool{"paired": false})
}
func (s *Server) pairPhone(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Admin string `json:"admin"`
		Relay string `json:"relay"`
	}
	if !readJSON(w, r, &input, 8192) {
		return
	}
	epoch, ok := s.account.access()
	if !ok {
		http.Error(w, "请先解锁 Cage", 423)
		return
	}
	s.mu.Lock()
	origin := s.preferences.MobileRelay
	s.mu.Unlock()
	if origin == "" {
		origin = mobilelink.OfficialRelay
	}
	if input.Admin != "" && (origin == mobilelink.OfficialRelay || origin != input.Relay) {
		requestError(w, errors.New("请先保存自建中继地址"))
		return
	}
	p := &s.phones
	p.mu.Lock()
	if p.closed || p.record != nil {
		p.mu.Unlock()
		http.Error(w, "请先移除已配对的手机", 409)
		return
	}
	if p.pending != nil {
		p.pending()
	}
	ctx, cancel := context.WithTimeout(epoch, 2*time.Minute)
	p.pending = cancel
	p.status = "等待扫码"
	p.mu.Unlock()
	complete := false
	defer func() {
		if !complete {
			cancel()
		}
	}()
	room, err := mobilelink.CreateRoom(ctx, origin, input.Admin)
	if err != nil {
		requestError(w, errors.New("中继未能创建连接。请检查网络、自建地址和管理令牌。"))
		return
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		requestError(w, err)
		return
	}
	host, err := remotelink.IdentityFromKey(key)
	if err != nil {
		requestError(w, err)
		return
	}
	code, raw, err := mobilelink.NewPairCode(origin, room, host.Pin)
	if err != nil {
		requestError(w, err)
		return
	}
	png, err := qrcode.Encode(raw, qrcode.Medium, 384)
	if err != nil {
		requestError(w, errors.New("无法生成二维码"))
		return
	}
	complete = true
	go func() {
		defer cancel()
		err := mobilelink.AcceptPair(ctx, code, room.HostToken, host, func(pin string) (mobilelink.PairedHost, error) {
			// Commit trust in the same unlocked account epoch that issued the
			// code. A lock or logout must not race the on-disk enrollment.
			s.account.mu.Lock()
			defer s.account.mu.Unlock()
			p.mu.Lock()
			defer p.mu.Unlock()
			var result mobilelink.PairedHost
			if ctx.Err() != nil || p.record != nil || p.closed {
				return result, errors.New("配对已结束")
			}
			var token [32]byte
			if _, err := rand.Read(token[:]); err != nil {
				return result, err
			}
			record := phoneRecord{Seed: key.Seed(), Relay: origin, Pin: pin, Admin: input.Admin, Lookup: base64.RawURLEncoding.EncodeToString(token[:])}
			if err := writePrivate(s.phoneFile(), record); err != nil {
				return result, err
			}
			s.attachPhone(record)
			p.status = "手机已配对"
			return mobilelink.PairedHost{Version: 1, Relay: origin, HostPin: host.Pin, Lookup: record.Lookup}, nil
		})
		p.mu.Lock()
		defer p.mu.Unlock()
		if err != nil && ctx.Err() == nil && p.record == nil {
			p.status = "配对未完成，请重新生成二维码"
		}
	}()
	w.Header().Set("Cache-Control", "no-store")
	jsonReply(w, map[string]any{"image": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), "expires": room.ExpiresAt, "manual": "CAGE-" + base64.RawURLEncoding.EncodeToString([]byte(raw))})
}
