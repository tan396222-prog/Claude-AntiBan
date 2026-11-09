package types

import (
	"context"
	"github.com/miekg/dns"
	"net"
	"net/netip"
	"regexp"
)

// Outbound decides whether the guest may reach dst over network ("tcp" or
// "udp"): see Configuration.Outbound.
type Outbound func(network string, dst netip.AddrPort) bool

// Allows reports whether o lets the guest reach dst: all, when nil.
func (o Outbound) Allows(network string, dst netip.AddrPort) bool {
	return o == nil || o(network, dst)
}

type DialContext func(context.Context, string, string) (net.Conn, error)

type Configuration struct {
	DialContext DialContext                                       `yaml:"-"`
	DNSExchange func(context.Context, *dns.Msg) (*dns.Msg, error) `yaml:"-"`
	// Print packets on stderr
	Debug bool `yaml:"debug,omitempty"`

	// Record all packets coming in and out in a file that can be read by Wireshark (pcap)
	CaptureFile string `yaml:"capture-file,omitempty"`

	// Length of packet
	// Larger packets means less packets to exchange for the same amount of data (and less protocol overhead)
	MTU int `yaml:"mtu,omitempty"`

	// Network reserved for the virtual network
	Subnet string `yaml:"subnet,omitempty"`

	// IP address of the virtual gateway
	GatewayIP string `yaml:"gatewayIP,omitempty"`

	// MAC address of the virtual gateway
	GatewayMacAddress string `yaml:"gatewayMacAddress,omitempty"`

	// Built-in DNS records that will be served by the DNS server embedded in the gateway
	DNS []Zone `yaml:"dns,omitempty"`

	// List of search domains that will be added in all DHCP replies
	DNSSearchDomains []string `yaml:"dnsSearchDomains,omitempty"`

	// Port forwarding between the machine running the gateway and the virtual network.
	Forwards map[string]string `yaml:"forwards,omitempty"`

	// Address translation of incoming traffic.
	// Useful for reaching the host itself (localhost) from the virtual network.
	NAT map[string]string `yaml:"nat,omitempty"`

	// IPs assigned to the gateway that can answer to ARP requests
	GatewayVirtualIPs []string `yaml:"gatewayVirtualIPs,omitempty"`

	// DHCP static leases. Allow to assign pre-defined IP to virtual machine based on the MAC address
	DHCPStaticLeases map[string]string `yaml:"dhcpStaticLeases,omitempty"`

	// Only for Hyperkit
	// Allow to assign a pre-defined MAC address to an Hyperkit VM
	VpnKitUUIDMacAddresses map[string]string `yaml:"vpnKitUUIDMacAddresses,omitempty"`

	// Protocol to be used. Only for /connect mux
	Protocol Protocol `yaml:"-"`

	// EC2 Metadata Service Access
	Ec2MetadataAccess bool `yaml:"ec2MetadataAccess,omitempty"`

	// Outbound, when set, decides whether the virtual network opens a
	// connection the guest asks for: network is "tcp" or "udp", dst the
	// destination once NAT applied. A refused TCP connection is reset, a
	// refused UDP datagram dropped.
	Outbound Outbound `yaml:"-"`

	// DNSNames, when set, decides which names the gateway's resolver looks
	// up: any other (the name without its final dot) gets NXDOMAIN, and is
	// never asked of the host's resolver. This covers every query the
	// gateway receives, over UDP or TCP.
	DNSNames func(name string) bool `yaml:"-"`
	// DNSAnswered, when set, is told of the IPv4 addresses given for name,
	// so that Outbound can let the guest reach those only.
	DNSAnswered func(name string, ips []net.IP) `yaml:"-"`
}

type Protocol string

const (
	// HyperKitProtocol is handshake, then 16bits little endian size of packet, then the packet.
	HyperKitProtocol Protocol = "hyperkit"
	// QemuProtocol is 32bits big endian size of the packet, then the packet.
	QemuProtocol Protocol = "qemu"
	// BessProtocol transfers bare L2 packets as SOCK_SEQPACKET.
	BessProtocol Protocol = "bess"
	// StdioProtocol is HyperKitProtocol without the handshake
	StdioProtocol Protocol = "stdio"
	// VfkitProtocol transfers bare L2 packets as SOCK_DGRAM.
	VfkitProtocol Protocol = "vfkit"
)

type Zone struct {
	Name      string   `yaml:"name,omitempty"`
	Records   []Record `yaml:"records,omitempty"`
	DefaultIP net.IP   `yaml:"defaultIP,omitempty"`
}

type Record struct {
	Name   string         `yaml:"name,omitempty"`
	IP     net.IP         `yaml:"ip,omitempty"`
	Regexp *regexp.Regexp `json:",omitempty" yaml:"regexp,omitempty"`
}
