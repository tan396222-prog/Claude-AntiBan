package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sylvinus/agent-vm/internal/mobilelink"
)

// connectionFile is the only long-lived record exported to the phone.
// Its token may claim a transport role, but cannot authenticate either peer.
type connectionFile struct {
	Version     int    `json:"version"`
	Relay       string `json:"relay"`
	LookupToken string `json:"lookup_token"`
	HostPin     string `json:"host_pin"`
}

type hostSettings struct {
	Relay      string `json:"relay"`
	AdminFile  string `json:"admin_token_file"`
	CageBinary string `json:"cage_binary"`
	Config     string `json:"config"`
}

type savedHost struct {
	Connection connectionFile `json:"connection"`
	Settings   hostSettings   `json:"host"`
	PeerPin    string         `json:"peer_pin"`
}

func decodePrivateJSON(path string, result any) error {
	data, err := readPrivate(path, 16<<10)
	if err != nil {
		return err
	}
	defer clear(data)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(result) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid private connection record")
	}
	return nil
}

func (c connectionFile) valid() bool {
	raw, err := base64.RawURLEncoding.DecodeString(c.LookupToken)
	defer clear(raw)
	return c.Version == 1 && mobilelink.ValidRelayOrigin(c.Relay) && validPin(c.HostPin) && err == nil && len(raw) == 32 && base64.RawURLEncoding.EncodeToString(raw) == c.LookupToken
}

func (s hostSettings) valid() bool {
	return mobilelink.ValidRelayOrigin(s.Relay) && (s.AdminFile == "" || filepath.IsAbs(s.AdminFile)) && filepath.IsAbs(s.Config) && filepath.IsAbs(s.CageBinary)
}

func loadSavedHost(state, hostPin, peerPin string) (savedHost, error) {
	var h savedHost
	err := decodePrivateJSON(filepath.Join(state, "saved-host.json"), &h)
	if err != nil {
		return h, err
	}
	if !h.Connection.valid() || !h.Settings.valid() || h.Connection.Relay != h.Settings.Relay || h.Connection.HostPin != hostPin || h.PeerPin != peerPin {
		return savedHost{}, errors.New("saved host settings no longer match this pairing; verify the device state")
	}
	return h, nil
}

func configureSavedHost(state, dest, hostPin, peerPin string, settings hostSettings) (savedHost, error) {
	var h savedHost
	if !settings.valid() {
		return h, errors.New("saved host requires a trusted relay and absolute admin token, cage binary and config paths")
	}
	if info, err := os.Stat(settings.CageBinary); err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0) {
		return h, errors.New("saved Cage binary must be an executable file")
	}
	if _, err := readAdminToken(settings.AdminFile); err != nil {
		return h, err
	}
	path := filepath.Join(state, "saved-host.json")
	_, err := os.Lstat(path)
	switch {
	case err == nil:
		h, err = loadSavedHost(state, hostPin, peerPin)
		if err != nil {
			return h, err
		}
		if h.Settings != settings {
			return h, errors.New("existing saved host settings differ; revoke this pairing before replacing it")
		}
	case errors.Is(err, os.ErrNotExist):
		raw := make([]byte, 32)
		_, _ = rand.Read(raw)
		h = savedHost{Connection: connectionFile{Version: 1, Relay: settings.Relay, LookupToken: base64.RawURLEncoding.EncodeToString(raw), HostPin: hostPin}, Settings: settings, PeerPin: peerPin}
		clear(raw)
		data, _ := json.Marshal(h)
		err = writeNewPrivate(path, data)
		clear(data)
		if err != nil {
			return savedHost{}, err
		}
	default:
		return h, errors.New("cannot inspect saved host settings")
	}
	data, _ := json.Marshal(h.Connection)
	err = writeNewPrivate(dest, data)
	clear(data)
	return h, err
}

func readAdminToken(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	raw, err := readPrivate(path, 4098)
	if err != nil {
		return "", err
	}
	defer clear(raw)
	token := string(raw)
	if strings.HasSuffix(token, "\n") {
		token = strings.TrimSuffix(strings.TrimSuffix(token, "\n"), "\r")
	}
	if len(token) < 32 || len(token) > 4096 {
		return "", errors.New("invalid protected relay admin token")
	}
	for i := range len(token) {
		if token[i] < 0x21 || token[i] > 0x7e {
			return "", errors.New("invalid protected relay admin token")
		}
	}
	return token, nil
}

func revoke(state string) error {
	if _, err := loadIdentity(state); err != nil {
		return err
	}
	// Remove peer trust first, so an active saved bridge refuses access even
	// if a filesystem error interrupts deletion of the lookup credential.
	for _, name := range []string{"trusted-peer", "saved-host.json"} {
		if err := os.Remove(filepath.Join(state, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("could not revoke saved device state")
		}
	}
	return nil
}

var errRevoked = errors.New("saved device trust changed; host bridge closed")

func verifySaved(state string, h savedHost) bool {
	peer, err := trustedPeer(state)
	if err != nil || peer != h.PeerPin {
		return false
	}
	current, err := loadSavedHost(state, h.Connection.HostPin, peer)
	return err == nil && current == h
}

func monitorTrust(parent context.Context, verify func() bool) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	// Blocked TLS handshakes and already connected clients must also notice
	// revocation. A removed/replaced private trust record closes this bridge.
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !verify() {
					cancel(errRevoked)
					return
				}
			}
		}
	}()
	return ctx, func() { cancel(nil) }
}

func savedService(parent context.Context, state string, h savedHost, out io.Writer) error {
	ctx, stop := monitorTrust(parent, func() bool { return verifySaved(state, h) })
	defer stop()
	logger := log.New(out, "", 0)
	stopAwake := keepHostAwake(ctx, logger)
	defer stopAwake()
	delay := time.Second
	for ctx.Err() == nil {
		if !verifySaved(state, h) {
			return errRevoked
		}
		retry, err := savedRoom(ctx, state, h)
		if ctx.Err() != nil {
			break
		}
		if !retry {
			return err
		}
		if err == nil {
			delay = time.Second
		} else {
			logger.Println("Room unavailable; retrying the saved connection in", delay.String())
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return context.Cause(ctx)
		case <-timer.C:
		}
		if err != nil {
			delay = min(30*time.Second, 2*delay)
		}
	}
	return context.Cause(ctx)
}

func savedRoom(ctx context.Context, state string, h savedHost) (bool, error) {
	// Issue a fresh short-lived certificate and local Cage session for each
	// room. This also handles a restarted desktop control service.
	id, err := loadIdentity(state)
	if err != nil || id.Pin != h.Connection.HostPin {
		return false, errors.New("device identity changed")
	}
	if info, err := os.Stat(h.Settings.CageBinary); err != nil || !info.Mode().IsRegular() {
		return false, errors.New("saved Cage binary unavailable")
	}
	admin, err := readAdminToken(h.Settings.AdminFile)
	if err != nil {
		return false, err
	}
	b, err := localWorkbench(ctx, h.Settings.CageBinary, h.Settings.Config)
	if err != nil {
		return true, err
	}
	defer b.Close()
	room, err := mobilelink.CreateDiscoverableRoom(ctx, h.Settings.Relay, admin, h.Connection.LookupToken)
	if err != nil {
		return mobilelink.Retryable(err), err
	}
	err = mobilelink.ServeHost(ctx, h.Settings.Relay, room.ID, room.HostToken, id, h.PeerPin, b)
	if mobilelink.Retryable(err) && !time.Now().Before(room.ExpiresAt) {
		// An unused room reaching its normal pairing deadline is healthy.
		// Publish the next room without accumulating offline backoff.
		return true, nil
	}
	return err == nil || mobilelink.Retryable(err), err
}

func resolveSaved(ctx context.Context, path, base, peer string) (clientInvitation, error) {
	var c connectionFile
	if err := decodePrivateJSON(path, &c); err != nil {
		return clientInvitation{}, err
	}
	if !c.valid() || c.Relay != base || c.HostPin != peer {
		return clientInvitation{}, errors.New("connection file differs from independently trusted host or relay")
	}
	found, err := mobilelink.LookupRoom(ctx, base, c.LookupToken)
	if err != nil {
		return clientInvitation{}, err
	}
	return clientInvitation{found.ID, found.ClientToken, found.ExpiresAt}, nil
}
