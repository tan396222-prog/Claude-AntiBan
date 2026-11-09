package netguard

import (
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ap(s string) netip.AddrPort { return netip.MustParseAddrPort(s) }

// By default: the internet, never this machine, its networks, the VPN's.
func TestDefault(t *testing.T) {
	p := &Policy{}
	for dst, want := range map[string]bool{
		"1.1.1.1:443":             true,
		"[2606:4700::1111]:443":   true,
		"127.0.0.1:5432":          false,
		"[::1]:5432":              false,
		"[::ffff:127.0.0.1]:5432": false,
		"192.168.1.1:80":          false,
		"10.1.2.3:22":             false,
		"172.16.0.1:80":           false,
		"100.100.100.100:53":      false, // Tailscale
		"169.254.169.254:80":      false, // cloud metadata
		"[fd00::1]:80":            false,
		"[fe80::1]:80":            false,
		"224.0.0.251:5353":        false,
		"0.0.0.0:80":              false,
		"255.255.255.255:67":      false,
		"[::]:80":                 false,
	} {
		if got := p.Allows("tcp", ap(dst)); got != want {
			t.Errorf("%s: %v", dst, got)
		}
	}
}

// Ranges that never lead to the internet, and IPv6 forms that embed an IPv4
// address of this machine or its networks.
func TestReserved(t *testing.T) {
	p := &Policy{}
	for dst, want := range map[string]bool{
		"192.0.0.8:80":                              false, // IETF protocol assignments
		"192.0.2.1:80":                              false,
		"198.18.0.1:80":                             false, // benchmarking, proxies' fake IPs
		"198.19.255.255:80":                         false,
		"198.20.0.1:80":                             true,
		"198.51.100.1:80":                           false,
		"203.0.113.1:80":                            false,
		"240.0.0.1:80":                              false,
		"255.255.255.254:80":                        false,
		"239.255.255.250:1900":                      false,
		"100.64.0.1:80":                             false,
		"100.127.255.255:80":                        false,
		"100.128.0.1:80":                            true,
		"169.254.1.1:80":                            false,
		"[fec0::1]:80":                              false,
		"[ff02::1]:80":                              false,
		"[::127.0.0.1]:80":                          false, // IPv4-compatible
		"[::ffff:10.0.0.1]:80":                      false,
		"[::ffff:8.8.8.8]:80":                       true,
		"[64:ff9b::7f00:1]:80":                      false, // NAT64 of 127.0.0.1
		"[64:ff9b::c0a8:101]:80":                    false, // of 192.168.1.1
		"[64:ff9b::808:808]:80":                     true,  // of 8.8.8.8
		"[64:ff9b:1::a00:1]:80":                     false, // local-use NAT64
		"[2002:7f00:1::1]:80":                       false, // 6to4 of 127.0.0.1
		"[2002:a00:1::1]:80":                        false, // of 10.0.0.1
		"[2002:808:808::1]:80":                      true,  // of 8.8.8.8
		"[2001:0:4136:e378:8000:63bf:f7f7:f7f7]:80": true,  // Teredo, public server and client
		"[2001:0:4136:e378:8000:63bf:80ff:fffe]:80": false, // client 127.0.0.1
		"[2001:0:7f00:1:8000:63bf:f7f7:f7f7]:80":    false, // server 127.0.0.1
		"[2001:0:4136:e378:8000:63bf:f5ff:fffe]:80": false, // client 10.0.0.1
		"[2001:db8::1]:80":                          false,
		"[100::1]:80":                               false,
		"[2606:4700::1111]:443":                     true,
	} {
		if got := p.Allows("tcp", ap(dst)); got != want {
			t.Errorf("%s: %v", dst, got)
		}
	}
	// A zone does not hide a link-local address.
	if p.Allows("tcp", netip.AddrPortFrom(netip.MustParseAddr("fe80::1%en0"), 80)) {
		t.Error("zoned link-local")
	}
	// An allow line still opens one.
	q := &Policy{Allow: []Rule{{Net: netip.MustParsePrefix("198.18.0.0/15")}}}
	if !q.Allows("tcp", ap("198.18.0.5:80")) {
		t.Error("allowed benchmarking range")
	}
}

// Only tcp and udp are known: anything else is refused, unless open.
func TestNetwork(t *testing.T) {
	p := &Policy{}
	for network, want := range map[string]bool{"tcp": true, "udp": true, "icmp": false, "": false, "tcp4": false} {
		if got := p.Allows(network, ap("1.1.1.1:443")); got != want {
			t.Errorf("%q: %v", network, got)
		}
	}
	if !(&Policy{Open: true}).Allows("icmp", ap("127.0.0.1:1")) {
		t.Error("open")
	}
}

// What the user allows is reached, at its port only when one is given.
func TestAllow(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, FileName), []byte("# ollama on this machine\nallow localhost:11434\nallow 192.168.1.20\nallow 10.0.0.0/8 # lab\n"), 0o644)
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for dst, want := range map[string]bool{
		"127.0.0.1:11434": true,
		"[::1]:11434":     true,
		"127.0.0.1:5432":  false,
		"192.168.1.20:22": true,
		"192.168.1.21:22": false,
		"10.200.0.1:443":  true,
		"8.8.8.8:53":      true,
	} {
		if got := p.Allows("tcp", ap(dst)); got != want {
			t.Errorf("%s: %v", dst, got)
		}
	}
}

// The other forms the site documents: every loopback port, IPv6 with a
// port, a *. domain.
func TestForms(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, FileName), []byte("allow localhost\nallow [fd00::5]:80\ndomain *.github.com\n"), 0o644)
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for dst, want := range map[string]bool{
		"127.0.0.1:1":     true,
		"[::1]:65535":     true,
		"[fd00::5]:80":    true,
		"[fd00::5]:81":    false,
		"192.168.1.20:22": false,
	} {
		if got := p.Allows("udp", ap(dst)); got != want {
			t.Errorf("%s: %v", dst, got)
		}
	}
	if !p.Name("github.com") || !p.Name("api.github.com") || p.Name("example.com") {
		t.Error("*.github.com")
	}
}

func TestLoadErrors(t *testing.T) {
	for _, line := range []string{"allow", "allow nowhere", "allow 1.2.3.4:0", "deny 1.2.3.4", "domain a/b", "allow 1.2.3.4 5"} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, FileName), []byte(line+"\n"), 0o644)
		if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "line 1") {
			t.Errorf("%q: %v", line, err)
		}
	}
	p, err := Load(t.TempDir())
	if err != nil || p.Open || len(p.Allow) != 0 {
		t.Errorf("no file: %+v %v", p, err)
	}
}

// With domains: those names only, and only the addresses given for them.
func TestDomains(t *testing.T) {
	p := &Policy{Domains: []string{"github.com", "npmjs.org"}}
	for name, want := range map[string]bool{"github.com": true, "api.github.com.": true, "GitHub.com": true, "evilgithub.com": false, "example.com": false, "registry.npmjs.org": true} {
		if p.Name(name) != want {
			t.Errorf("Name(%q) != %v", name, want)
		}
	}
	if p.Allows("tcp", ap("140.82.112.3:443")) {
		t.Error("an address no allowed name gave")
	}
	p.Answered("github.com", []net.IP{net.ParseIP("140.82.112.3")})
	p.Answered("example.com", []net.IP{net.ParseIP("93.184.216.34")})
	if !p.Allows("tcp", ap("140.82.112.3:443")) || p.Allows("tcp", ap("93.184.216.34:443")) {
		t.Error("answered addresses")
	}
	// Never this machine, though a name gives it.
	p.Answered("github.com", []net.IP{net.ParseIP("127.0.0.1")})
	if p.Allows("tcp", ap("127.0.0.1:443")) {
		t.Error("loopback through an allowed name")
	}
}

func TestOpen(t *testing.T) {
	t.Setenv("AGENT_VM_UNSAFE_OPEN_NETWORK", "1")
	p, _ := Load(t.TempDir())
	if !p.Allows("tcp", ap("127.0.0.1:5432")) {
		t.Error("open")
	}
}

// This machine's own addresses, a public one included.
func TestOwnAddress(t *testing.T) {
	addrs, _ := net.InterfaceAddrs()
	p := &Policy{}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			ip, _ := netip.AddrFromSlice(n.IP)
			if p.Allows("tcp", netip.AddrPortFrom(ip.Unmap(), 80)) {
				t.Errorf("own address %s reachable", ip)
			}
		}
	}
}
