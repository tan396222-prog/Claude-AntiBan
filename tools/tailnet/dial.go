package main

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
)

type dialRecord struct {
	Address string `json:"address"`
	Token   string `json:"token"`
}

func generateHostKey() ([]byte, error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(key, "")
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(block), nil
}
func (n *node) serveDial(listener net.Listener) error {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return err
	}
	record := dialRecord{listener.Addr().String(), hex.EncodeToString(token)}
	b, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(n.dir, "control.json"), b, 0600); err != nil {
		return err
	}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 4096}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The existing SSH transport uses Bearer authentication. Standard proxy
		// clients use Proxy-Authorization so credentials never reach the peer.
		proxyAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("cage:"+record.Token))
		standardProxy := subtle.ConstantTimeCompare([]byte(r.Header.Get("Proxy-Authorization")), []byte(proxyAuth)) == 1
		controlClient := r.Method == "CONNECT" && r.Header.Get("Origin") == "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+record.Token)) == 1
		if !standardProxy && !controlClient {
			w.Header().Set("Proxy-Authenticate", `Basic realm="Cage Tailscale"`)
			http.Error(w, "Proxy authentication required", http.StatusProxyAuthRequired)
			return
		}
		if r.Method != "CONNECT" {
			n.proxyHTTP(w, r)
			return
		}
		host, port, err := net.SplitHostPort(r.Host)
		number, _ := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			http.Error(w, "Invalid target", 400)
			return
		}
		remote, err := n.dialPeer(r.Context(), host, port)
		if err != nil {
			http.Error(w, "Tailnet connection failed", 502)
			return
		}
		local, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			remote.Close()
			return
		}
		n.mu.Lock()
		peer := remote.(*peerConnection)
		if n.server != peer.server || !n.connections[peer.Conn] {
			n.mu.Unlock()
			local.Close()
			remote.Close()
			return
		}
		n.connections[local] = true
		n.mu.Unlock()
		defer func() {
			local.Close()
			remote.Close()
			n.mu.Lock()
			delete(n.connections, local)
			n.mu.Unlock()
		}()
		_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		if buffer.Flush() != nil {
			return
		}
		relayConnection(local, remote, buffer)
	})
	go func() { _ = server.Serve(listener) }()
	return nil
}
func dial(host, port string) error {
	b, err := os.ReadFile(filepath.Join(dataDir(), "control.json"))
	if err != nil {
		return err
	}
	var record dialRecord
	if json.Unmarshal(b, &record) != nil {
		return errors.New("invalid control record")
	}
	address, _, err := net.SplitHostPort(record.Address)
	if err != nil || address != "127.0.0.1" {
		return errors.New("invalid control address")
	}
	c, err := net.DialTimeout("tcp", record.Address, 3*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	if !safeHost(host) {
		return errors.New("invalid destination")
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return errors.New("invalid port")
	}
	_, err = fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\n\r\n", net.JoinHostPort(host, port), net.JoinHostPort(host, port), record.Token)
	if err != nil {
		return err
	}
	reader := bufio.NewReader(c)
	res, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
	if err != nil {
		return err
	}
	if res.StatusCode != 200 {
		return errors.New("tailnet connection refused")
	}
	_ = c.SetDeadline(time.Time{})
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(c, os.Stdin)
		if tcp, ok := c.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		close(done)
	}()
	_, err = io.Copy(os.Stdout, reader)
	return err
}
