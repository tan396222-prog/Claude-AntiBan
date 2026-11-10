package cage

import (
	"bytes"
	"encoding/binary"
	"net/netip"
)

const MacIPv6ProbePort = 49417
const MacIPv6ProbePrefix = "Cage IPv6 link probe v1 "

// ObserveDroppedIPv6 is called at the actual VM switch, before forwarding,
// only when its instantiated network stack has no IPv6 protocol. Validate the
// complete datagram; ambient NDP, stale checks or another VM cannot attest it.
func (g *EgressGate) ObserveDroppedIPv6(packet []byte) {
	if len(packet) < 48 || packet[0]>>4 != 6 || packet[6] != 17 || packet[7] != 1 {
		return
	}
	n := int(binary.BigEndian.Uint16(packet[4:6]))
	if n+40 != len(packet) || n != int(binary.BigEndian.Uint16(packet[44:46])) || binary.BigEndian.Uint16(packet[42:44]) != MacIPv6ProbePort || binary.BigEndian.Uint16(packet[46:48]) == 0 {
		return
	}
	src := netip.AddrFrom16([16]byte(packet[8:24]))
	dst := netip.AddrFrom16([16]byte(packet[24:40]))
	if !src.IsLinkLocalUnicast() || dst != netip.MustParseAddr("ff02::2") || !bytes.HasPrefix(packet[48:], []byte(MacIPv6ProbePrefix)) {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state.Mode != GateChecking || !g.validLocked(g.epoch) || g.state.CheckID == "" {
		return
	}
	want := MacIPv6ProbePrefix + g.state.RunID + " " + g.state.CheckID
	if string(packet[48:]) == want {
		g.state.IPv6LinkProbeCheck = g.state.CheckID
	}
}
