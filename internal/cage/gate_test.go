package cage

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fixtureSafetyReport(g *EgressGate) SafetyReport {
	s := g.Status()
	r := SafetyReport{Policy: SafetyPolicyVersion, ID: "fixture-only", RunID: s.RunID, CheckID: s.CheckID, Configuration: s.Configuration, CreatedAt: time.Now()}
	for _, id := range []string{"egress_policy", "proxy_transport", "dns_transport", "guest_settings", "browser_settings", "filesystem_boundary", "management_boundary", "fail_closed", "udp_policy", "ipv6_policy", "exit_consistency"} {
		r.Checks = append(r.Checks, SafetyCheck{ID: id, Title: id, Critical: true, State: CheckPassed, Evidence: "synthetic unit-test evidence", CheckedAt: r.CreatedAt})
	}
	return r
}

func fixtureGate(t *testing.T) *EgressGate {
	t.Helper()
	cfg := DefaultConfig()
	cfg.ProxyURL = "http://127.0.0.1:9"
	g, err := NewEgressGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close("test complete") })
	return g
}

func fixtureGrant(t *testing.T, g *EgressGate) SafetyReport {
	t.Helper()
	if err := g.BeginCheck(); err != nil {
		t.Fatal(err)
	}
	r := fixtureSafetyReport(g)
	if err := g.Grant(r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSafetyReportRequiresCompleteCurrentEvidence(t *testing.T) {
	g := fixtureGate(t)
	if err := g.BeginCheck(); err != nil {
		t.Fatal(err)
	}
	valid := fixtureSafetyReport(g)
	for name, change := range map[string]func(*SafetyReport){
		"empty":               func(r *SafetyReport) { r.Checks = nil },
		"dns missing":         func(r *SafetyReport) { r.Checks = append(r.Checks[:2], r.Checks[3:]...) },
		"failed":              func(r *SafetyReport) { r.Checks[0].State = CheckFailed },
		"unknown":             func(r *SafetyReport) { r.Checks[0].State = CheckUnknown },
		"downgraded critical": func(r *SafetyReport) { r.Checks[0].Critical = false },
		"missing evidence":    func(r *SafetyReport) { r.Checks[0].Evidence = "" },
		"duplicate":           func(r *SafetyReport) { r.Checks = append(r.Checks, r.Checks[0]) },
		"old VM":              func(r *SafetyReport) { r.RunID = "old-run" },
		"other configuration": func(r *SafetyReport) { r.Configuration = "other" },
		"expired":             func(r *SafetyReport) { r.CreatedAt = time.Now().Add(-25 * time.Hour) },
	} {
		t.Run(name, func(t *testing.T) {
			r := valid
			r.Checks = append([]SafetyCheck(nil), valid.Checks...)
			change(&r)
			if r.Authorizes(valid.RunID, valid.Configuration, time.Now()) == nil {
				t.Fatal("invalid evidence authorized networking")
			}
		})
	}
	valid.Checks = append(valid.Checks, SafetyCheck{ID: "reputation", Critical: false, State: CheckFailed})
	if err := valid.Authorizes(valid.RunID, valid.Configuration, time.Now()); err != nil {
		t.Fatalf("advisory blocked networking: %v", err)
	}
}

func TestGateStartsClosedAndOldReportCannotReopen(t *testing.T) {
	g := fixtureGate(t)
	public := netip.MustParseAddrPort("8.8.8.8:443")
	if g.Allows("tcp", public) {
		t.Fatal("new VM started online")
	}
	if _, err := g.DialContext(context.Background(), "tcp", public.String()); err == nil {
		t.Fatal("direct dial bypassed closed gate")
	}
	r := fixtureGrant(t, g)
	if !g.Allows("tcp", public) {
		t.Fatal("valid report did not enable proxy TCP")
	}
	if g.Allows("udp", public) || g.Allows("tcp", netip.MustParseAddrPort("127.0.0.1:17777")) {
		t.Fatal("grant weakened isolation policy")
	}
	if g.Allows("tcp", netip.MustParseAddrPort("[2606:4700:4700::1111]:443")) {
		t.Fatal("IPv6 bypassed the measured IPv4 policy")
	}
	g.Close("failure")
	if g.Renew(r.ID) == nil || g.Grant(r) == nil {
		t.Fatal("revoked report reopened networking")
	}
	if err := g.BeginCheck(); err != nil {
		t.Fatal(err)
	}
	if g.Grant(r) == nil {
		t.Fatal("report from previous check session replayed")
	}
}

func TestRenewCannotOutliveItsReport(t *testing.T) {
	g := fixtureGate(t)
	r := fixtureGrant(t, g)
	expiry := time.Now().Add(time.Second)
	g.mu.Lock()
	g.state.Report.CreatedAt = expiry.Add(-SafetyReportLifetime)
	g.mu.Unlock()
	if err := g.Renew(r.ID); err != nil {
		t.Fatal(err)
	}
	if g.Status().Until.After(expiry) {
		t.Fatal("renewal exceeded report lifetime")
	}
}

func TestGateRevokesEstablishedTunnelAndLease(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != "8.8.8.8:443" {
			w.WriteHeader(400)
			return
		}
		conn, reader, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = reader.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = reader.Flush()
		_, _ = io.Copy(conn, reader)
	}))
	defer proxy.Close()
	cfg := DefaultConfig()
	cfg.ProxyURL = proxy.URL
	g, err := NewEgressGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close("test complete")
	fixtureGrant(t, g)
	conn, err := g.DialContext(context.Background(), "tcp", "8.8.8.8:443")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	_, _ = conn.Write([]byte("ping"))
	var b [4]byte
	if _, err = io.ReadFull(conn, b[:]); err != nil || string(b[:]) != "ping" {
		t.Fatal("proxy tunnel did not transport bytes", err)
	}
	g.mu.Lock()
	g.armLocked(20 * time.Millisecond)
	g.mu.Unlock()
	deadline := time.Now().Add(time.Second)
	for g.Status().Mode != GateClosed && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if _, err = conn.Write([]byte("must not send")); err == nil {
		t.Fatal("expired lease retained established connection")
	}
	if g.Status().Mode != GateClosed {
		t.Fatal("expired gate stayed open")
	}
}

func TestGateCancelsStalledProxyHandshake(t *testing.T) {
	entered := make(chan struct{})
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		close(entered)
		_, _ = io.Copy(io.Discard, conn)
	}))
	defer proxy.Close()
	cfg := DefaultConfig()
	cfg.ProxyURL = proxy.URL
	g, err := NewEgressGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close("test complete")
	fixtureGrant(t, g)
	done := make(chan error, 1)
	go func() {
		c, e := g.DialContext(context.Background(), "tcp", "8.8.8.8:443")
		if c != nil {
			c.Close()
		}
		done <- e
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("CONNECT did not reach fixture")
	}
	g.Close("revoke while dialing")
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("revoked dial succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("revoke did not interrupt CONNECT handshake")
	}
}

func TestDiagnosticServiceCannotForwardArbitraryInput(t *testing.T) {
	var requests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(502) }))
	defer proxy.Close()
	cfg := DefaultConfig()
	cfg.ProxyURL = proxy.URL
	g, err := NewEgressGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close("test complete")
	if err = g.BeginCheck(); err != nil {
		t.Fatal(err)
	}
	for _, request := range []string{
		"CONNECT attacker.example:443 HTTP/1.1\r\nHost: 198.18.0.254\r\n\r\n",
		"GET /egress?secret=value HTTP/1.1\r\nHost: 198.18.0.254\r\n\r\n",
		"GET /egress HTTP/1.1\r\nHost: attacker.example\r\n\r\n",
	} {
		conn, err := g.DialContext(context.Background(), "tcp", DiagnosticAddress)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		_, _ = io.WriteString(conn, request)
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		conn.Close()
		if err != nil || resp.StatusCode != 400 {
			t.Fatal("unsafe probe input accepted", err)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("untrusted request reached upstream")
	}
	if err = g.SetFault(true); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/egress", "/dns"} {
		conn, err := g.DialContext(context.Background(), "tcp", DiagnosticAddress)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		_, _ = io.WriteString(conn, "GET "+path+" HTTP/1.1\r\nHost: 198.18.0.254\r\n\r\n")
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		conn.Close()
		if err != nil || resp.StatusCode != 503 {
			t.Fatal("fault probe did not fail closed", err)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("fault drill contacted upstream")
	}
	if s := g.Status(); s.Counters.FaultTCP != 1 || s.Counters.FaultDNS != 1 {
		t.Fatal("fault path was not exercised")
	}
}

func TestGateControlAuthenticationAndConfigInvalidation(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.ProxyURL = "http://127.0.0.1:9"
	path := filepath.Join(dir, "config.json")
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	g, err := NewEgressGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	stop, err := StartGateControl(path, dir, g)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	client, err := OpenGateClient(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	bad := *client
	bad.record.Token = strings.Repeat("0", 64)
	if _, err = bad.Status(context.Background()); err == nil {
		t.Fatal("unauthenticated control accepted")
	}
	fixtureGrant(t, g)
	cfg.Timezone = "Europe/London"
	data, _ = json.Marshal(cfg)
	_ = os.WriteFile(path, data, 0600)
	deadline := time.Now().Add(2 * time.Second)
	for g.Status().Mode != GateClosed && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if g.Status().Mode != GateClosed {
		t.Fatal("changed configuration retained network authorization")
	}
}
