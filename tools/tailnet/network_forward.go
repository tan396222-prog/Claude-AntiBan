package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"tailscale.com/tsnet"
)

type forwardState struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Address string `json:"address"`
}

type tailnetForward struct {
	forwardState
	server   *tsnet.Server
	listener net.Listener
}

// openForward, closeForwards and forwardsSnapshot run under n.mu, like snapshot.
// All application data stays inside the guest; only a loopback port is opened.
func (n *node) openForward(ctx context.Context, host string, port int) (string, error) {
	if port < 1 || port > 65535 {
		return "", errors.New("服务端口必须介于 1 和 65535 之间")
	}
	out, err := n.snapshot(ctx)
	if err != nil {
		return "", err
	}
	if n.server == nil || out.Backend != "Running" {
		return "", errors.New("请先连接 Tailscale")
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	ip := ""
	for _, peer := range out.Peers {
		name := strings.ToLower(peer.Name)
		short, _, _ := strings.Cut(name, ".")
		if host == peer.IP || host == name || host == short {
			ip = peer.IP
			break
		}
	}
	if ip == "" {
		return "", errors.New("设备未出现在当前 Tailscale 网络中")
	}
	key := net.JoinHostPort(ip, strconv.Itoa(port))
	if previous := n.forwards[key]; previous != nil && previous.server == n.server {
		return previous.Address, nil
	}
	if len(n.forwards) >= 32 {
		return "", errors.New("最多同时连接 32 个私网服务，请断开 Tailscale 后重试")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	forward := &tailnetForward{forwardState: forwardState{Host: ip, Port: port, Address: listener.Addr().String()}, server: n.server, listener: listener}
	if n.forwards == nil {
		n.forwards = make(map[string]*tailnetForward)
	}
	n.forwards[key] = forward
	go n.acceptForward(key, forward)
	return forward.Address, nil
}

func (n *node) closeForwards() {
	for _, forward := range n.forwards {
		_ = forward.listener.Close()
	}
	clear(n.forwards)
}

func (n *node) forwardsSnapshot() []forwardState {
	result := make([]forwardState, 0, len(n.forwards))
	for _, forward := range n.forwards {
		if forward.server == n.server {
			result = append(result, forward.forwardState)
		}
	}
	slices.SortFunc(result, func(a, b forwardState) int {
		if a.Host != b.Host {
			return strings.Compare(a.Host, b.Host)
		}
		return a.Port - b.Port
	})
	return result
}

func (n *node) proxyURL() string {
	if n.server == nil {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(n.dir, "control.json"))
	if err != nil {
		return ""
	}
	var record dialRecord
	if json.Unmarshal(b, &record) != nil || record.Token == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(record.Address)
	if err != nil || host != "127.0.0.1" {
		return ""
	}
	return (&url.URL{Scheme: "http", Host: record.Address, User: url.UserPassword("cage", record.Token)}).String()
}

func (n *node) acceptForward(key string, forward *tailnetForward) {
	for {
		local, err := forward.listener.Accept()
		if err != nil {
			return
		}
		go n.forwardConnection(key, forward, local)
	}
}

func (n *node) forwardConnection(key string, forward *tailnetForward, local net.Conn) {
	n.mu.Lock()
	if n.server != forward.server || n.forwards[key] != forward {
		n.mu.Unlock()
		local.Close()
		return
	}
	n.connections[local] = true
	n.mu.Unlock()
	defer func() { local.Close(); n.mu.Lock(); delete(n.connections, local); n.mu.Unlock() }()
	remote, err := n.dialPeerOn(context.Background(), forward.Host, strconv.Itoa(forward.Port), forward.server)
	if err != nil {
		return
	}
	defer remote.Close()
	relayConnection(local, remote, local)
}
