package mobilelink

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sylvinus/agent-vm/internal/relay"
	"github.com/sylvinus/agent-vm/internal/remotelink"
)

func TestScanEnrollsOnlyHolderOfOriginalCode(t *testing.T) {
	const admin = "private-test-credential-for-custom-relay"
	service, err := relay.New(relay.Config{AdminToken: admin})
	if err != nil {
		t.Fatal(err)
	}
	edge := httptest.NewServer(service)
	defer edge.Close()
	defer service.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	desktop, err := remotelink.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	phone, err := remotelink.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	for _, tampered := range []bool{true, false} {
		room, err := CreateRoom(ctx, edge.URL, admin)
		if err != nil {
			t.Fatal(err)
		}
		code, raw, err := NewPairCode(edge.URL, room, desktop.Pin)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		var enrolled string
		go func() {
			done <- AcceptPair(ctx, code, room.HostToken, desktop, func(pin string) (PairedHost, error) {
				enrolled = pin
				return PairedHost{1, edge.URL, desktop.Pin, strings.Repeat("A", 43)}, nil
			})
		}()
		if _, err := RedeemPair(ctx, raw, OfficialRelay, phone.Pin); err == nil {
			t.Fatal("accepted unexpected relay")
		}
		if tampered {
			changed := code
			changed.Seed = append([]byte(nil), code.Seed...)
			changed.Seed[0] ^= 1
			payload, _ := json.Marshal(changed)
			raw = "CAGE1:" + string(payload)
		}
		got, clientErr := RedeemPair(ctx, raw, edge.URL, phone.Pin)
		hostErr := <-done
		if tampered {
			if enrolled != "" || hostErr == nil || clientErr == nil {
				t.Fatal("modified optical secret enrolled a device")
			}
			continue
		}
		var saved PairedHost
		if hostErr != nil || clientErr != nil || enrolled != phone.Pin || json.Unmarshal([]byte(got), &saved) != nil || saved.HostPin != desktop.Pin {
			t.Fatal("valid optical pairing failed", hostErr, clientErr)
		}
		expired := code
		expired.Expires = time.Now().Add(-time.Second).Unix()
		if _, err = expired.identity(); err == nil {
			t.Fatal("expired optical pairing accepted")
		}
	}
}
