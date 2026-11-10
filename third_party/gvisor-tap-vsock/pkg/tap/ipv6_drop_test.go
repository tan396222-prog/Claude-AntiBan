package tap

import (
	"bytes"
	"context"
	"testing"
)

// The observer must run on data read from the guest, before forwarding the
// frame to any other peer or to the IPv4 stack.
func TestIPv6DropAtGuestIngress(t *testing.T) {
	s := NewSwitch(false)
	payload := []byte{0x60, 0, 0, 0, 0, 0, 17, 1}
	ethernet := make([]byte, 14)
	ethernet[12], ethernet[13] = 0x86, 0xdd
	var seen []byte
	s.SetDroppedIPv6(func(packet []byte) { seen = append(seen, packet...) })
	s.rxBuf(context.Background(), 1, append(ethernet, payload...))
	if !bytes.Equal(seen, payload) || s.Sent != 0 || s.Received != uint64(len(ethernet)+len(payload)) {
		t.Fatal("IPv6 frame was not observed and dropped at ingress")
	}
}
