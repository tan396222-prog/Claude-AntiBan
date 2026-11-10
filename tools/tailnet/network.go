package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tailscale.com/tsnet"
)

// dialPeer is deliberately limited to visible tailnet peers. In particular the
// proxy never falls back to a guest/host socket for public, loopback or LAN IPs.
func (n *node) dialPeer(ctx context.Context, host, port string) (net.Conn, error) {
	return n.dialPeerOn(ctx, host, port, nil)
}

func (n *node) dialPeerOn(ctx context.Context, host, port string, expected *tsnet.Server) (net.Conn, error) {
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return nil, errors.New("invalid tailnet port")
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	n.mu.Lock()
	out, err := n.snapshot(ctx)
	ts := n.server
	target := ""
	if err == nil && ts != nil && (expected == nil || expected == ts) && out.Backend == "Running" {
		for _, p := range out.Peers {
			name := strings.ToLower(p.Name)
			short, _, _ := strings.Cut(name, ".")
			if host == p.IP || host == name || host == short {
				target = net.JoinHostPort(p.IP, port)
				break
			}
		}
	}
	n.mu.Unlock()
	if target == "" {
		return nil, errors.New("unknown tailnet device or disconnected")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	remote, err := ts.Dial(ctx, "tcp", target)
	if err != nil {
		return nil, err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.server != ts {
		remote.Close()
		return nil, errors.New("tailnet disconnected")
	}
	n.connections[remote] = true
	return &peerConnection{Conn: remote, node: n, server: ts}, nil
}

type peerConnection struct {
	net.Conn
	node   *node
	server *tsnet.Server
}

func (c *peerConnection) Close() error {
	err := c.Conn.Close()
	c.node.mu.Lock()
	delete(c.node.connections, c.Conn)
	c.node.mu.Unlock()
	return err
}

func (c *peerConnection) CloseWrite() error {
	if tcp, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return tcp.CloseWrite()
	}
	return c.Close()
}

// A FIN in one direction must not discard bytes still flowing back. Both
// sockets stay tracked until both halves finish or the node revokes them.
func relayConnection(local net.Conn, remote net.Conn, input io.Reader) {
	copyHalf := func(dst net.Conn, src io.Reader) {
		if _, err := io.Copy(dst, src); err != nil {
			_ = dst.Close()
			return
		}
		if tcp, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = tcp.CloseWrite()
		} else {
			_ = dst.Close()
		}
	}
	done := make(chan struct{})
	go func() { copyHalf(remote, input); close(done) }()
	copyHalf(local, remote)
	<-done
}

func stripProxyHeaders(h http.Header) {
	for _, name := range strings.Split(h.Get("Connection"), ",") {
		h.Del(strings.TrimSpace(name))
	}
	for _, name := range []string{"Connection", "Proxy-Connection", "Proxy-Authorization", "Proxy-Authenticate", "Keep-Alive", "TE", "Trailer", "Transfer-Encoding", "Upgrade"} {
		h.Del(name)
	}
}

func (n *node) proxyHTTP(w http.ResponseWriter, r *http.Request) {
	if !r.URL.IsAbs() || r.URL.Scheme != "http" || r.URL.User != nil || r.URL.Host != r.Host || r.URL.Fragment != "" {
		http.Error(w, "Expected an absolute HTTP proxy URL", http.StatusBadRequest)
		return
	}
	port := r.URL.Port()
	if port == "" {
		port = "80"
	}
	remote, err := n.dialPeer(r.Context(), r.URL.Hostname(), port)
	if err != nil {
		http.Error(w, "Tailnet device unavailable", http.StatusBadGateway)
		return
	}
	defer remote.Close()
	// One connection per request avoids keeping connections across a logout or
	// a device identity change. The node tracks it so disconnect closes streams.
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: 15 * time.Second,
		DialContext: func(context.Context, string, string) (net.Conn, error) { return remote, nil },
	}
	defer transport.CloseIdleConnections()
	request := r.Clone(r.Context())
	request.RequestURI = ""
	request.Header = r.Header.Clone()
	stripProxyHeaders(request.Header)
	response, err := transport.RoundTrip(request)
	if err != nil {
		http.Error(w, "Tailnet service did not respond", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	stripProxyHeaders(response.Header)
	for key, values := range response.Header {
		w.Header()[key] = values
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(proxyResponseWriter{w}, response.Body)
}

// Flush incremental responses so tailnet web UIs can use SSE and streaming.
type proxyResponseWriter struct{ http.ResponseWriter }

func (w proxyResponseWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if n > 0 {
		_ = http.NewResponseController(w.ResponseWriter).Flush()
	}
	return n, err
}
