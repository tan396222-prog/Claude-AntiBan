package managedproxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/miekg/dns"
)

// BootstrapClient is for explicit infrastructure downloads only. It ignores
// environment proxies and resolves public hosts over IP-addressed DoH URLs.
func BootstrapClient() *http.Client {
	tr := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ip, err := netip.ParseAddr(host)
		if err != nil {
			ip, err = resolveInfrastructure(ctx, host)
			if err != nil {
				return nil, err
			}
		}
		if !publicIP(ip) {
			return nil, errors.New("订阅服务器必须使用公网地址")
		}
		return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
	}, IdleConnTimeout: 10 * time.Second}
	return &http.Client{Transport: tr, Timeout: 30 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 3 || r.URL.Scheme != "https" || r.URL.User != nil {
			return errors.New("订阅重定向被拒绝")
		}
		return nil
	}}
}
func usageSummary(raw string) string {
	parts := []string{}
	for _, field := range strings.Split(raw, ";") {
		kv := strings.SplitN(strings.TrimSpace(field), "=", 2)
		if len(kv) != 2 {
			continue
		}
		if kv[0] != "upload" && kv[0] != "download" && kv[0] != "total" && kv[0] != "expire" {
			continue
		}
		if _, err := strconv.ParseUint(kv[1], 10, 64); err == nil {
			parts = append(parts, kv[0]+"="+kv[1])
		}
	}
	return strings.Join(parts, "; ")
}
func publicIP(ip netip.Addr) bool {
	return ip.Is4() && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !netip.MustParsePrefix("100.64.0.0/10").Contains(ip) && !netip.MustParsePrefix("198.18.0.0/15").Contains(ip)
}

// BootstrapDNSURLs is shared by host downloads and the core's node resolver.
// Numeric HTTPS endpoints need neither the host resolver nor a working proxy.
func BootstrapDNSURLs() []string {
	return []string{"https://1.1.1.1/dns-query", "https://223.5.5.5/dns-query"}
}

func resolveInfrastructure(ctx context.Context, host string) (netip.Addr, error) {
	tr := &http.Transport{Proxy: nil}
	defer tr.CloseIdleConnections()
	c := &http.Client{Transport: tr, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("DNS redirect refused") }}
	return resolveInfrastructureWith(ctx, host, c, BootstrapDNSURLs())
}

func resolveInfrastructureWith(ctx context.Context, host string, client *http.Client, servers []string) (netip.Addr, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	q := new(dns.Msg)
	q.SetQuestion(dns.Fqdn(host), dns.TypeA)
	wire, err := q.Pack()
	if err != nil {
		return netip.Addr{}, errors.New("代理基础设施域名无效")
	}
	type result struct {
		ip  netip.Addr
		err error
	}
	results := make(chan result, len(servers))
	// A blocked resolver must not delay a reachable one. Cancel the remaining
	// HTTPS requests once a usable public answer is available.
	for _, server := range servers {
		go func() {
			ip, err := queryInfrastructureDNS(ctx, client, server, q, wire)
			results <- result{ip, err}
		}()
	}
	for range servers {
		select {
		case <-ctx.Done():
			return netip.Addr{}, ctx.Err()
		case result := <-results:
			if result.err == nil {
				return result.ip, nil
			}
		}
	}
	return netip.Addr{}, errors.New("代理基础设施 DNS 均不可用或未返回公网 IPv4；请检查主机网络后重试，未使用系统 DNS 回退")
}

func queryInfrastructureDNS(ctx context.Context, client *http.Client, server string, q *dns.Msg, wire []byte) (netip.Addr, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", server, bytes.NewReader(wire))
	if err != nil {
		return netip.Addr{}, err
	}
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")
	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, errors.New("代理基础设施域名解析失败")
	}
	defer resp.Body.Close()
	wire, err = io.ReadAll(io.LimitReader(resp.Body, 65536))
	var answer dns.Msg
	if err != nil || resp.StatusCode != 200 || answer.Unpack(wire) != nil || answer.Id != q.Id ||
		!answer.Response || answer.Truncated || answer.Rcode != dns.RcodeSuccess || len(answer.Question) != 1 ||
		!strings.EqualFold(answer.Question[0].Name, q.Question[0].Name) || answer.Question[0].Qtype != dns.TypeA || answer.Question[0].Qclass != dns.ClassINET {
		return netip.Addr{}, errors.New("代理基础设施 DNS 响应无效")
	}
	for _, rr := range answer.Answer {
		if a, ok := rr.(*dns.A); ok {
			if ip, ok := netip.AddrFromSlice(a.A); ok && publicIP(ip.Unmap()) {
				return ip.Unmap(), nil
			}
		}
	}
	return netip.Addr{}, errors.New("代理基础设施域名没有可用公网 IPv4")
}
func Fetch(ctx context.Context, client *http.Client, rawURL string) ([]byte, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return nil, "", errors.New("订阅需要完整的 HTTPS 网址，不支持地址内用户名和密码")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, "", errors.New("订阅网址无效")
	}
	req.Header.Set("User-Agent", "clash.meta/Cage")
	res, err := client.Do(req)
	if err != nil {
		return nil, "", errors.New("订阅下载失败，请检查下载路径和服务地址")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, "", errors.New("订阅服务器返回 HTTP " + strconv.Itoa(res.StatusCode))
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, MaxImport+1))
	if err != nil || len(b) > MaxImport {
		return nil, "", errors.New("订阅下载中断或超过 8 MiB")
	}
	usage := "" // Keep only known numeric metadata; never echo arbitrary headers.
	if raw := res.Header.Get("Subscription-Userinfo"); len(raw) < 300 {
		usage = usageSummary(raw)
	}
	return b, usage, nil
}
