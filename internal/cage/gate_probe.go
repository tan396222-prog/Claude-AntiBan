package cage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/miekg/dns"
)

// ProbeExitIP sends a fixed request through the configured proxy. It does not
// include user headers, credentials, a report or arbitrary guest data.
func ProbeExitIP(ctx context.Context, cfg Config) (string, error) {
	e, err := NewEgress(cfg)
	if err != nil {
		return "", err
	}
	return probeExitIP(ctx, e)
}

func probeExitIP(ctx context.Context, e *Egress) (string, error) {
	transport := &http.Transport{Proxy: nil, DialContext: e.DialContext, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("probe redirect refused") }}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.ipify.org?format=json", nil)
	req.Header.Set("User-Agent", "Cage-network-check/1")
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("无法经指定代理读取检测出口")
	}
	defer resp.Body.Close()
	var data struct {
		IP string `json:"ip"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&data) != nil {
		return "", errors.New("出口检测服务没有返回有效结果")
	}
	ip, err := netip.ParseAddr(data.IP)
	if err != nil || !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return "", errors.New("出口检测结果不是有效公网 IPv4 地址")
	}
	return ip.String(), nil
}

func probeDNS(ctx context.Context, e *Egress) error {
	q := new(dns.Msg)
	q.SetQuestion("example.com.", dns.TypeA)
	r, err := e.ExchangeDNS(ctx, q)
	if err != nil {
		return err
	}
	if r.Rcode != dns.RcodeSuccess {
		return errors.New("DNS probe did not resolve its fixed target")
	}
	for _, answer := range r.Answer {
		if a, ok := answer.(*dns.A); ok && a.A.IsGlobalUnicast() && !a.A.IsPrivate() {
			return nil
		}
	}
	return errors.New("DNS probe returned no usable answer")
}

func (g *EgressGate) dialDiagnostic(ctx context.Context) (net.Conn, error) {
	_, done, epoch, err := g.operation(ctx, true)
	if err != nil {
		return nil, err
	}
	defer done()
	client, server := net.Pipe()
	tracked, err := g.track(client, epoch)
	if err != nil {
		server.Close()
		return nil, err
	}
	g.mu.Lock()
	if !g.validLocked(epoch) {
		g.mu.Unlock()
		_ = tracked.Close()
		_ = server.Close()
		return nil, ErrGateClosed
	}
	parent, fault := g.epochContext, g.state.Fault
	g.mu.Unlock()
	go g.serveDiagnostic(parent, server, fault)
	return tracked, nil
}

// This synthetic HTTP service terminates guest input locally. It constructs
// fixed upstream probes instead of forwarding a CONNECT target, URL, query,
// body or headers. It is not a proxy to allowlisted shared CDN addresses.
func (g *EgressGate) serveDiagnostic(parent context.Context, conn net.Conn, fault bool) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	ctx, cancel := context.WithTimeout(parent, 14*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	req, err := http.ReadRequest(bufio.NewReader(io.LimitReader(conn, 8192)))
	if err != nil {
		return
	}
	defer req.Body.Close()
	host := strings.TrimSuffix(strings.ToLower(req.Host), ":80")
	if req.Method != "GET" || host != DiagnosticHost || req.URL.IsAbs() || req.URL.RawQuery != "" || req.ContentLength > 0 || len(req.TransferEncoding) != 0 ||
		(req.URL.Path != "/" && req.URL.Path != "/egress" && req.URL.Path != "/dns") {
		writeDiagnostic(conn, 400, map[string]string{"error": "fixed_probe_only"})
		return
	}
	if req.URL.Path == "/" {
		writeDiagnostic(conn, 200, map[string]string{"service": "cage-diagnostic"})
		return
	}
	g.mu.Lock()
	if g.probeRequests >= 32 {
		g.mu.Unlock()
		writeDiagnostic(conn, 429, map[string]string{"error": "probe_limit"})
		return
	}
	g.probeRequests++
	g.mu.Unlock()
	e := g.diagnostic
	if fault {
		e = g.failedDiagnostic
	}
	if req.URL.Path == "/dns" {
		err = probeDNS(ctx, e)
		g.mu.Lock()
		g.state.Counters.ProbeDNS++
		if fault && err != nil {
			g.state.Counters.FaultDNS++
		}
		g.mu.Unlock()
		if err != nil {
			writeDiagnostic(conn, 503, map[string]string{"error": "dns_unavailable"})
			return
		}
		writeDiagnostic(conn, 200, map[string]bool{"ok": true})
		return
	}
	ip, err := probeExitIP(ctx, e)
	g.mu.Lock()
	g.state.Counters.ProbeTCP++
	if fault && err != nil {
		g.state.Counters.FaultTCP++
	}
	g.mu.Unlock()
	if err != nil {
		writeDiagnostic(conn, 503, map[string]string{"error": "proxy_unavailable"})
		return
	}
	writeDiagnostic(conn, 200, map[string]string{"ip": ip})
}

func writeDiagnostic(conn net.Conn, status int, value any) {
	body, _ := json.Marshal(value)
	resp := http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)), ProtoMajor: 1, ProtoMinor: 1,
		Header: http.Header{"Content-Type": {"application/json"}, "Cache-Control": {"no-store"}, "Access-Control-Allow-Origin": {"*"}, "Connection": {"close"}},
		Body:   io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Close: true}
	_ = resp.Write(conn)
}
