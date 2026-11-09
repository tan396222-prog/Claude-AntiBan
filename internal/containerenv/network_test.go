package containerenv

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/miekg/dns"
	"github.com/sylvinus/agent-vm/internal/cage"
)

func TestFramesRejectInvalidLengthsAndTruncation(t *testing.T) {
	for _, size := range []uint32{0, 13, maxFrameSize + 1, 0xffffffff} {
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], size)
		if _, err := readFrame(bytes.NewReader(header[:]), make([]byte, maxFrameSize)); err == nil {
			t.Fatalf("accepted frame size %d", size)
		}
	}
	var stream bytes.Buffer
	frame := bytes.Repeat([]byte{1}, 64)
	if err := writeFrame(&stream, frame); err != nil {
		t.Fatal(err)
	}
	if _, err := readFrame(bytes.NewReader(stream.Bytes()[:stream.Len()-1]), make([]byte, maxFrameSize)); err != io.ErrUnexpectedEOF {
		t.Fatalf("truncated frame: %v", err)
	}
}

func TestContainerNetworkARPAndClosedDNS(t *testing.T) {
	network, err := NewNetwork(cage.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	host, guest := net.Pipe()
	defer guest.Close()
	done := make(chan error, 1)
	go func() { done <- network.Run(ctx, host) }()
	defer func() {
		cancel()
		guest.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("network did not stop")
		}
	}()
	_ = guest.SetDeadline(time.Now().Add(3 * time.Second))
	guestMAC, _ := net.ParseMAC(GuestMAC)
	gatewayMAC, _ := net.ParseMAC(GatewayMAC)
	arp := &layers.ARP{AddrType: layers.LinkTypeEthernet, Protocol: layers.EthernetTypeIPv4, HwAddressSize: 6, ProtAddressSize: 4, Operation: layers.ARPRequest, SourceHwAddress: guestMAC, SourceProtAddress: net.ParseIP(GuestIP).To4(), DstHwAddress: make([]byte, 6), DstProtAddress: net.ParseIP(GatewayIP).To4()}
	ethernet := &layers.Ethernet{SrcMAC: guestMAC, DstMAC: net.HardwareAddr{255, 255, 255, 255, 255, 255}, EthernetType: layers.EthernetTypeARP}
	buffer := gopacket.NewSerializeBuffer()
	if err := gopacket.SerializeLayers(buffer, gopacket.SerializeOptions{FixLengths: true}, ethernet, arp); err != nil {
		t.Fatal(err)
	}
	if err := writeFrame(guest, buffer.Bytes()); err != nil {
		t.Fatal(err)
	}
	response, err := readFrame(guest, make([]byte, maxFrameSize))
	if err != nil {
		t.Fatal(err)
	}
	packet := gopacket.NewPacket(response, layers.LayerTypeEthernet, gopacket.Default)
	answer, ok := packet.Layer(layers.LayerTypeARP).(*layers.ARP)
	if !ok || answer.Operation != layers.ARPReply || !bytes.Equal(answer.SourceHwAddress, gatewayMAC) {
		t.Fatal("virtual gateway did not answer ARP")
	}

	query := new(dns.Msg)
	query.SetQuestion("example.com.", dns.TypeA)
	payload, _ := query.Pack()
	ip := &layers.IPv4{Version: 4, IHL: 5, TTL: 64, Protocol: layers.IPProtocolUDP, SrcIP: net.ParseIP(GuestIP), DstIP: net.ParseIP(GatewayIP)}
	udp := &layers.UDP{SrcPort: 41000, DstPort: 53}
	_ = udp.SetNetworkLayerForChecksum(ip)
	ethernet = &layers.Ethernet{SrcMAC: guestMAC, DstMAC: gatewayMAC, EthernetType: layers.EthernetTypeIPv4}
	buffer = gopacket.NewSerializeBuffer()
	if err := gopacket.SerializeLayers(buffer, gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}, ethernet, ip, udp, gopacket.Payload(payload)); err != nil {
		t.Fatal(err)
	}
	if err := writeFrame(guest, buffer.Bytes()); err != nil {
		t.Fatal(err)
	}
	response, err = readFrame(guest, make([]byte, maxFrameSize))
	if err != nil {
		t.Fatal(err)
	}
	packet = gopacket.NewPacket(response, layers.LayerTypeEthernet, gopacket.Default)
	dnsLayer := packet.Layer(layers.LayerTypeDNS)
	if dnsLayer == nil {
		t.Fatalf("expected DNS refusal, got %v", packet)
	}
	if network.Gate.Status().Counters.DNSDenied == 0 {
		t.Fatal("DNS bypassed the closed host gate")
	}
}

func TestHostRejectsGuestMalformedFrames(t *testing.T) {
	for _, size := range []uint32{0, 13, 0xffffffff} {
		network, err := NewNetwork(cage.DefaultConfig())
		if err != nil {
			t.Fatal(err)
		}
		host, guest := net.Pipe()
		done := make(chan error, 1)
		go func() { done <- network.Run(context.Background(), host) }()
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], size)
		_, _ = guest.Write(header[:])
		guest.Close()
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("accepted size %d", size)
			}
		case <-time.After(time.Second):
			t.Fatal("malformed guest frame blocked host")
		}
	}
}

func TestProcessEndpointClosesIdleProcess(t *testing.T) {
	if os.Getenv("CAGE_CONTAINER_PIPE_TEST") == "1" {
		_, _ = io.Copy(os.Stdout, os.Stdin)
		os.Exit(0)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestProcessEndpointClosesIdleProcess$")
	command.Env = append(os.Environ(), "CAGE_CONTAINER_PIPE_TEST=1")
	endpoint, wait, err := ProcessEndpoint(command)
	if err != nil {
		t.Fatal(err)
	}
	_ = endpoint.SetDeadline(time.Now().Add(3 * time.Second))
	done := make(chan error, 1)
	go func() { _, err := endpoint.Write([]byte("frame")); done <- err }()
	got := make([]byte, 5)
	if _, err := io.ReadFull(endpoint, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "frame" {
		t.Fatalf("pipe payload %q", got)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	endpoint.Close()
	waited := make(chan struct{})
	go func() { _ = wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("idle helper was not reaped")
	}
}
