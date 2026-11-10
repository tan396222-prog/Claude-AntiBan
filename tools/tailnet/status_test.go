package main

import (
	"net/netip"
	"testing"
	"time"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

func TestStatusShowsAccountNetworkAndDevicesWithoutPublicKeys(t *testing.T) {
	seen := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	var out state
	applyStatus(&out, &ipnstate.Status{
		BackendState:   "Running",
		TailscaleIPs:   []netip.Addr{netip.MustParseAddr("100.88.2.3")},
		CurrentTailnet: &ipnstate.TailnetStatus{Name: "work.test"},
		Self:           &ipnstate.PeerStatus{HostName: "cage-mac", DNSName: "cage-mac.test.ts.net.", OS: "macOS", UserID: 17},
		User:           map[tailcfg.UserID]tailcfg.UserProfile{17: {LoginName: "owner@test.invalid"}},
		Health:         []string{"Control connection interrupted"},
		Peer: map[key.NodePublic]*ipnstate.PeerStatus{
			key.NewNode().Public(): {ID: "offline", HostName: "a-server", DNSName: "a-server.test.ts.net.", OS: "linux", TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.88.2.5")}, LastSeen: seen},
			key.NewNode().Public(): {ID: "online", HostName: "z-phone", DNSName: "z-phone.test.ts.net.", OS: "android", TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.88.2.4")}, Online: true},
		},
	})
	if out.Tailnet != "work.test" || out.Account != "owner@test.invalid" || out.Hostname != "cage-mac" || out.OS != "macOS" || out.Name != "cage-mac.test.ts.net" {
		t.Fatalf("missing network identity: %+v", out)
	}
	if len(out.Peers) != 2 || !out.Peers[0].Online || out.Peers[0].OS != "android" || out.Peers[1].LastSeen == nil || !out.Peers[1].LastSeen.Equal(seen) {
		t.Fatalf("incorrect devices: %+v", out.Peers)
	}
	if len(out.Health) != 1 || len(out.Keys) != 0 {
		t.Fatal("network status must not require SSH setup")
	}
}
