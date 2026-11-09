package managedproxy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
	"go.yaml.in/yaml/v3"
)

func writeDNSAnswer(t *testing.T, w http.ResponseWriter, r *http.Request, mutate func(*dns.Msg)) {
	t.Helper()
	if r.Method != "POST" || r.Header.Get("Content-Type") != "application/dns-message" || r.Header.Get("Accept") != "application/dns-message" {
		t.Error("bootstrap changed its encrypted DNS request contract")
	}
	wire, _ := io.ReadAll(r.Body)
	var q dns.Msg
	if err := q.Unpack(wire); err != nil || len(q.Question) != 1 || q.Question[0].Qtype != dns.TypeA {
		t.Error("invalid bootstrap question", err)
		w.WriteHeader(400)
		return
	}
	answer := new(dns.Msg)
	answer.SetReply(&q)
	answer.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.ParseIP("8.8.8.8").To4()}}
	if mutate != nil {
		mutate(answer)
	}
	wire, err := answer.Pack()
	if err != nil {
		t.Error(err)
	}
	w.Header().Set("Content-Type", "application/dns-message")
	_, _ = w.Write(wire)
}

func TestInfrastructureDNSDoesNotWaitForBlockedResolver(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/blocked" {
			_, _ = io.Copy(io.Discard, r.Body)
			close(started)
			select {
			case <-r.Context().Done():
				close(canceled)
			case <-release:
			}
			return
		}
		select {
		case <-started:
		case <-r.Context().Done():
			return
		}
		writeDNSAnswer(t, w, r, nil)
	}))
	defer server.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ip, err := resolveInfrastructureWith(ctx, "example.com", server.Client(), []string{server.URL + "/blocked", server.URL + "/working"})
	if err != nil || ip.String() != "8.8.8.8" {
		t.Fatal("reachable resolver could not bypass blocked one", ip, err)
	}
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("unused DNS query was not canceled")
	}
}

func TestInfrastructureDNSRejectsInvalidAnswersAndTriesOtherResolver(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*dns.Msg)
	}{
		{"wrong ID", func(m *dns.Msg) { m.Id++ }},
		{"not a reply", func(m *dns.Msg) { m.Response = false }},
		{"NXDOMAIN", func(m *dns.Msg) { m.Rcode = dns.RcodeNameError }},
		{"truncated", func(m *dns.Msg) { m.Truncated = true }},
		{"wrong question", func(m *dns.Msg) { m.Question[0].Name = "other.example." }},
		{"private IP", func(m *dns.Msg) { m.Answer[0].(*dns.A).A = net.ParseIP("127.0.0.1").To4() }},
		{"no answer", func(m *dns.Msg) { m.Answer = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mutate := tc.mutate
				if r.URL.Path == "/working" {
					mutate = nil
				}
				writeDNSAnswer(t, w, r, mutate)
			}))
			defer server.Close()
			ctx := context.Background()
			if _, err := resolveInfrastructureWith(ctx, "example.com", server.Client(), []string{server.URL + "/invalid"}); err == nil {
				t.Fatal("invalid DNS response was accepted")
			}
			ip, err := resolveInfrastructureWith(ctx, "example.com", server.Client(), []string{server.URL + "/invalid", server.URL + "/working"})
			if err != nil || ip.String() != "8.8.8.8" {
				t.Fatal("valid DNS alternative failed", ip, err)
			}
		})
	}
}

func TestInfrastructureDNSAllUnavailableAndCancellation(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer server.Close()
	servers := []string{server.URL + "/one", server.URL + "/two"}
	if ip, err := resolveInfrastructureWith(context.Background(), "example.com", server.Client(), servers); err == nil || ip.IsValid() || !strings.Contains(err.Error(), "未使用系统 DNS 回退") {
		t.Fatal("all failed resolvers must fail closed", ip, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := resolveInfrastructureWith(ctx, "example.com", server.Client(), servers); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation was not preserved", err)
	}
}

func TestRuntimeUsesRedundantBootstrapWithoutChangingBusinessDNS(t *testing.T) {
	s, _ := Load(t.TempDir())
	if err := s.Import("test", "", []byte(testNodes), "source", 0, ""); err != nil {
		t.Fatal(err)
	}
	s.Selected = s.Sources[0].Nodes[0].ID
	wire, err := s.Runtime(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		DNS struct {
			Default []string `yaml:"default-nameserver"`
			Proxy   []string `yaml:"proxy-server-nameserver"`
			Names   []string `yaml:"nameserver"`
		} `yaml:"dns"`
		Rules []string `yaml:"rules"`
	}
	if err := yaml.Unmarshal(wire, &config); err != nil {
		t.Fatal(err)
	}
	want := BootstrapDNSURLs()
	if len(want) < 2 || !reflect.DeepEqual(config.DNS.Default, want) || !reflect.DeepEqual(config.DNS.Proxy, want) {
		t.Fatal("node bootstrap did not receive both DNS services", config.DNS)
	}
	if !reflect.DeepEqual(config.DNS.Names, []string{"https://1.1.1.1/dns-query#" + Group}) || !reflect.DeepEqual(config.Rules, []string{"MATCH," + Group}) {
		t.Fatal("bootstrap fallback changed business routing", config)
	}
}

func TestInfrastructureDNSLiveIntegration(t *testing.T) {
	if os.Getenv("CAGE_TEST_BOOTSTRAP") != "1" {
		t.Skip("explicit host-network DNS test required")
	}
	ip, err := resolveInfrastructure(context.Background(), "example.com")
	if err != nil || !publicIP(ip) {
		t.Fatal("redundant encrypted DNS lookup failed", err)
	}
	// Inspect each service separately as a diagnostic. One may be unreachable
	// on this network; successful combined resolution is the required result.
	for _, endpoint := range BootstrapDNSURLs() {
		t.Run(endpoint, func(t *testing.T) {
			tr := &http.Transport{Proxy: nil}
			defer tr.CloseIdleConnections()
			client := &http.Client{Transport: tr, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
			ip, err := resolveInfrastructureWith(context.Background(), "example.com", client, []string{endpoint})
			if err != nil || !publicIP(ip) {
				t.Log("DNS endpoint unavailable on this network", err)
				return
			}
			t.Log("DNS endpoint returned a usable public IPv4")
		})
	}
}
