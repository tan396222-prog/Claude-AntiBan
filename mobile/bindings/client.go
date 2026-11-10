// Package cagemobile exposes the native signing and foreground connection API
// to generated Java and Objective-C code. gomobile's generated module must be
// able to import this public package; the implementation stays internal.
package cagemobile

import (
	"context"
	"encoding/json"
	"time"

	"github.com/sylvinus/agent-vm/internal/mobilelink"
	"github.com/sylvinus/agent-vm/internal/mobilelink/nativeclient"
)

func DefaultRelay() string { return mobilelink.OfficialRelay }

type DeviceSigner interface {
	PublicKeyDER() ([]byte, error)
	SignSHA256(digest []byte) ([]byte, error)
}

func Fingerprint(device DeviceSigner) (string, error) { return nativeclient.Fingerprint(device) }

type Client struct{ native *nativeclient.Client }

func Open(relayOrigin, roomID, clientToken, trustedHostPin string, device DeviceSigner) (*Client, error) {
	client, err := nativeclient.Open(relayOrigin, roomID, clientToken, trustedHostPin, device)
	if err != nil {
		return nil, err
	}
	return &Client{native: client}, nil
}

func OpenSaved(relayOrigin, lookupToken, trustedHostPin string, device DeviceSigner) (*Client, error) {
	client, err := nativeclient.OpenSaved(relayOrigin, lookupToken, trustedHostPin, device)
	if err != nil {
		return nil, err
	}
	return &Client{native: client}, nil
}

func (c *Client) StartUI(directory string) (string, error) { return c.native.StartUI(directory) }
func (c *Client) Close() error                             { return c.native.Close() }

// ReportDevice is optional metadata for the desktop pairing card. An older host
// can refuse the operation without preventing the phone from opening its UI.
func (c *Client) ReportDevice(platform, model string) {
	body, _ := json.Marshal(mobilelink.DeviceInfo{Platform: platform, Model: mobilelink.DeviceModel(platform, model)})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = c.native.Do(ctx, "POST", "/api/mobile/device", body)
}

func Pair(code, relay string, device DeviceSigner) (string, error) {
	pin, err := Fingerprint(device)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	return mobilelink.RedeemPair(ctx, code, relay, pin)
}
