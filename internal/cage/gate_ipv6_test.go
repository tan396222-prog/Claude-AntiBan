package cage

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

func TestMacIPv6DropRequiresThisCheckingInstance(t *testing.T) {
	g := fixtureGate(t)
	if err := g.BeginCheck(); err != nil {
		t.Fatal(err)
	}
	s := g.Status()
	payload := []byte(MacIPv6ProbePrefix + s.RunID + " " + s.CheckID)
	packet := make([]byte, 48+len(payload))
	packet[0] = 0x60
	packet[6] = 17
	packet[7] = 1
	binary.BigEndian.PutUint16(packet[4:6], uint16(8+len(payload)))
	a := netip.MustParseAddr("fe80::1").As16()
	copy(packet[8:24], a[:])
	a = netip.MustParseAddr("ff02::2").As16()
	copy(packet[24:40], a[:])
	binary.BigEndian.PutUint16(packet[42:44], MacIPv6ProbePort)
	binary.BigEndian.PutUint16(packet[44:46], uint16(8+len(payload)))
	packet[47] = 1
	copy(packet[48:], payload)
	for _, position := range []int{0, 5, 6, 7, 8, 24, 42, 45, 48, len(packet) - 1} {
		wrong := append([]byte(nil), packet...)
		wrong[position] ^= 0xff
		g.ObserveDroppedIPv6(wrong)
		if g.Status().IPv6LinkProbeCheck != "" {
			t.Fatalf("unrelated datagram accepted (byte %d)", position)
		}
	}
	g.ObserveDroppedIPv6(packet)
	if g.Status().IPv6LinkProbeCheck != s.CheckID {
		t.Fatal("matching received datagram not recorded")
	}
	g.Close("reset")
	if err := g.BeginCheck(); err != nil {
		t.Fatal(err)
	}
	g.ObserveDroppedIPv6(packet)
	if g.Status().IPv6LinkProbeCheck != "" {
		t.Fatal("replayed earlier check")
	}
}
