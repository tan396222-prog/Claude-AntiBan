package mobilelink

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/sylvinus/agent-vm/internal/relay"
	"github.com/sylvinus/agent-vm/internal/remotelink"
)

// The optical channel transfers a temporary TLS client seed and the pinned
// server identity. Neither seed nor persistent lookup token goes to the relay.
type PairCode struct {
	Relay   string `json:"r,omitempty"`
	Room    string `json:"i"`
	Token   string `json:"t"`
	Seed    []byte `json:"s"`
	Host    string `json:"h"`
	Expires int64  `json:"e"`
}
type PairedHost struct {
	Version int    `json:"version"`
	Relay   string `json:"relay"`
	HostPin string `json:"host_pin"`
	Lookup  string `json:"lookup_token"`
}

func NewPairCode(origin string, room relay.Invitation, host string) (PairCode, string, error) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return PairCode{}, "", err
	}
	code := PairCode{Relay: origin, Room: room.ID, Token: room.ClientToken, Seed: seed, Host: host, Expires: room.ExpiresAt.Unix()}
	if origin == OfficialRelay {
		code.Relay = ""
	}
	b, err := json.Marshal(code)
	return code, "CAGE1:" + string(b), err
}
func (p PairCode) origin() string {
	if p.Relay == "" {
		return OfficialRelay
	}
	return p.Relay
}
func pairPin(pin string) bool {
	b, e := base64.StdEncoding.DecodeString(strings.TrimPrefix(pin, "sha256:"))
	return e == nil && len(b) == 32 && pin == "sha256:"+base64.StdEncoding.EncodeToString(b)
}
func (p PairCode) identity() (*remotelink.Identity, error) {
	if len(p.Seed) != ed25519.SeedSize || !pairPin(p.Host) || !validCapability(p.Room) || !validCapability(p.Token) || !time.Now().Before(time.Unix(p.Expires, 0)) {
		return nil, errors.New("配对码无效或已过期")
	}
	return remotelink.IdentityFromKey(ed25519.NewKeyFromSeed(p.Seed))
}
func pairDeadline(ctx context.Context, c *tls.Conn) func() {
	deadline, _ := ctx.Deadline()
	_ = c.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { c.NetConn().Close() })
	return func() { stop(); c.NetConn().Close() }
}

// AcceptPair registers a single phone over a dedicated bootstrap tunnel. The
// regular workbench tunnel accepts only the phone's hardware-backed identity.
func AcceptPair(ctx context.Context, p PairCode, hostToken string, host *remotelink.Identity, save func(string) (PairedHost, error)) error {
	temp, err := p.identity()
	if err != nil {
		return err
	}
	ws, err := join(ctx, p.origin(), p.Room, "host", hostToken)
	if err != nil {
		return err
	}
	conn, err := remotelink.ServerAfterJoin(ctx, ws, host, temp.Pin)
	if err != nil {
		return err
	}
	defer pairDeadline(ctx, conn)()
	var pin string
	if err = json.NewDecoder(io.LimitReader(conn, 256)).Decode(&pin); err != nil || !pairPin(pin) {
		return errors.New("手机身份无效")
	}
	record, err := save(pin)
	if err != nil {
		return err
	}
	return json.NewEncoder(conn).Encode(record)
}
func RedeemPair(ctx context.Context, raw, selectedRelay, phonePin string) (string, error) {
	var p PairCode
	raw = strings.TrimSpace(raw)
	if len(raw) <= 4096 && strings.HasPrefix(raw, "CAGE-") {
		b, err := base64.RawURLEncoding.DecodeString(raw[5:])
		if err != nil {
			return "", errors.New("配对码格式无效")
		}
		raw = string(b)
	}
	if len(raw) > 4096 || !strings.HasPrefix(raw, "CAGE1:") || json.Unmarshal([]byte(raw[6:]), &p) != nil || !pairPin(phonePin) {
		return "", errors.New("请扫描 Cage 电脑端的配对码")
	}
	if p.origin() != selectedRelay || !ValidRelayOrigin(selectedRelay) {
		return "", errors.New("中继与当前设置不符，请检查自建中继地址")
	}
	id, err := p.identity()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithDeadline(ctx, time.Unix(p.Expires, 0))
	defer cancel()
	ws, err := join(ctx, selectedRelay, p.Room, "client", p.Token)
	if err != nil {
		return "", err
	}
	conn, err := remotelink.Client(ctx, ws, id, p.Host)
	if err != nil {
		return "", err
	}
	defer pairDeadline(ctx, conn)()
	if err = json.NewEncoder(conn).Encode(phonePin); err != nil {
		return "", err
	}
	var record PairedHost
	if json.NewDecoder(io.LimitReader(conn, 4096)).Decode(&record) != nil || record.Version != 1 || record.HostPin != p.Host || record.Relay != selectedRelay || !validCapability(record.Lookup) {
		return "", errors.New("电脑未完成配对，请刷新二维码")
	}
	b, err := json.Marshal(record)
	return string(b), err
}
