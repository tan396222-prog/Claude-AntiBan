package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	gliderssh "github.com/gliderlabs/ssh"
	"github.com/pkg/sftp"
	gossh "golang.org/x/crypto/ssh"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"tailscale.com/envknob"
	"tailscale.com/net/netns"
	"tailscale.com/tailcfg"
	"tailscale.com/tsnet"
	"tailscale.com/tstest/integration"
	"tailscale.com/tstest/integration/testcontrol"
	"tailscale.com/types/logger"
	"testing"
	"time"
)

func TestBidirectionalSSHAndPersistentIdentity(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("TS_NO_LOGS_NO_SUPPORT", "true")
	envknob.SetenvForTest(t, "TS_DEBUG_ALWAYS_USE_DERP", "true")
	netns.SetEnabled(false)
	defer netns.SetEnabled(true)
	control := &testcontrol.Server{DERPMap: integration.RunDERPAndSTUN(t, logger.Discard, "127.0.0.1"), MagicDNSDomain: "cage-test.ts.net", DNSConfig: &tailcfg.DNSConfig{Proxied: true}, Logf: logger.Discard}
	server := httptest.NewUnstartedServer(control)
	control.HTTPTestServer = server
	server.Start()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dir := filepath.Join(t.TempDir(), "cage")
	n, err := newNode(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = n.startServer(&tsnet.Server{Dir: dir, Hostname: "cage-test", ControlURL: server.URL, UserLogf: logger.Discard}); err != nil {
		t.Fatal(err)
	}
	defer func() { n.mu.Lock(); n.stop(); n.mu.Unlock() }()
	n.config.Enabled = true
	cageStatus, err := n.server.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}
	remote := &tsnet.Server{Dir: t.TempDir(), Hostname: "host-test", ControlURL: server.URL, UserLogf: logger.Discard}
	defer remote.Close()
	hostStatus, err := remote.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}
	waitDERP(t, ctx, n.server)
	waitDERP(t, ctx, remote)
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, _ := gossh.NewSignerFromKey(private)
	key := strings.TrimSpace(string(gossh.MarshalAuthorizedKey(signer.PublicKey()))) + " local-test"
	if _, err = n.run(ctx, request{Action: "add-key", Key: key}); err != nil {
		t.Fatal(err)
	}
	login := &gossh.ClientConfig{User: currentUser(), Auth: []gossh.AuthMethod{gossh.PublicKeys(signer)}, HostKeyCallback: gossh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second}
	incoming := func(config *gossh.ClientConfig) (*gossh.Client, error) {
		c, err := remote.Dial(ctx, "tcp", net.JoinHostPort(cageStatus.TailscaleIPs[0].String(), "22"))
		if err != nil {
			return nil, err
		}
		_ = c.SetDeadline(time.Now().Add(8 * time.Second))
		conn, channels, requests, err := gossh.NewClientConn(c, "cage-test:22", config)
		if err != nil {
			c.Close()
			return nil, err
		}
		_ = c.SetDeadline(time.Time{})
		return gossh.NewClient(conn, channels, requests), nil
	}
	var client *gossh.Client
	for ctx.Err() == nil {
		client, err = incoming(login)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(err)
		case <-time.After(150 * time.Millisecond):
		}
	}
	if client == nil {
		t.Fatal("incoming SSH never connected")
	}
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	command := "printf cage-inbound-ok"
	if runtime.GOOS == "windows" {
		command = "[Console]::Write('cage-inbound-ok')"
	}
	out, err := session.Output(command)
	session.Close()
	if err != nil || string(out) != "cage-inbound-ok" {
		t.Fatalf("incoming exec: %q %v", out, err)
	}

	interactive, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err = interactive.RequestPty("xterm-256color", 24, 80, gossh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	output, err := interactive.Output(command)
	_ = interactive.Close()
	if err != nil || !strings.Contains(string(output), "cage-inbound-ok") {
		t.Fatalf("interactive terminal: %q %v", output, err)
	}
	// SFTP and local forwarding support a real editor's SSH transport.
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		t.Fatal(err)
	}
	remoteFile := filepath.ToSlash(filepath.Join(os.Getenv("HOME"), "sftp-check.txt"))
	file, err := sftpClient.Create(remoteFile)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.Write([]byte("sftp-ok"))
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(filepath.FromSlash(remoteFile))
	if err != nil || string(actual) != "sftp-ok" {
		t.Fatalf("sftp: %q %v", actual, err)
	}
	_ = sftpClient.Close()
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		c, e := echo.Accept()
		if e == nil {
			defer c.Close()
			_, _ = io.Copy(c, c)
		}
	}()
	forwarded, err := client.Dial("tcp", echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = forwarded.Write([]byte("tcp-forward"))
	probe := make([]byte, 11)
	if _, err = io.ReadFull(forwarded, probe); err != nil || string(probe) != "tcp-forward" {
		t.Fatalf("forwarding: %q %v", probe, err)
	}
	_ = forwarded.Close()
	client.Close()
	hostListener, err := remote.Listen("tcp", ":22")
	if err != nil {
		t.Fatal(err)
	}
	hostSSH := &gliderssh.Server{PublicKeyHandler: func(_ gliderssh.Context, k gliderssh.PublicKey) bool {
		return gliderssh.KeysEqual(k, signer.PublicKey())
	}, Handler: func(s gliderssh.Session) { _, _ = io.WriteString(s, "host-outbound-ok"); _ = s.Exit(0) }}
	go func() { _ = hostSSH.Serve(hostListener) }()
	defer hostSSH.Close()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err = n.serveDial(listener); err != nil {
		t.Fatal(err)
	}
	var snapshot state
	for ctx.Err() == nil {
		snapshot, err = n.run(ctx, request{Action: "status"})
		if err == nil && len(snapshot.Peers) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(snapshot.Peers) == 0 {
		t.Fatal("peer not discovered")
	}
	var record dialRecord
	b, _ := os.ReadFile(filepath.Join(n.dir, "control.json"))
	_ = json.Unmarshal(b, &record)
	tunnel, err := net.Dial("tcp", record.Address)
	if err != nil {
		t.Fatal(err)
	}
	defer tunnel.Close()
	_ = tunnel.SetDeadline(time.Now().Add(10 * time.Second))
	target := net.JoinHostPort(hostStatus.TailscaleIPs[0].String(), "22")
	_, _ = fmt.Fprintf(tunnel, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\n\r\n", target, target, record.Token)
	reader := bufio.NewReader(tunnel)
	response, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("outbound tunnel: %v %v", response, err)
	}
	conn, channels, requests, err := gossh.NewClientConn(&bufferedConnection{Conn: tunnel, reader: reader}, "host:22", login)
	if err != nil {
		t.Fatal(err)
	}
	outgoing := gossh.NewClient(conn, channels, requests)
	session, err = outgoing.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	out, err = session.Output("test")
	if err != nil || string(out) != "host-outbound-ok" {
		t.Fatalf("outgoing exec: %q %v", out, err)
	}
	outgoing.Close()
	if _, err = n.run(ctx, request{Action: "remove-key", Key: key}); err != nil {
		t.Fatal(err)
	}
	if denied, e := incoming(login); e == nil {
		denied.Close()
		t.Fatal("removed SSH key accepted")
	}
	if _, err = n.run(ctx, request{Action: "add-key", Key: key}); err != nil {
		t.Fatal(err)
	}
	config, _ := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".ssh", "config"))
	if !strings.HasPrefix(string(config), "Include ") {
		t.Fatal("guest ssh config not installed")
	}
	managed, _ := os.ReadFile(filepath.Join(n.dir, "ssh_config"))
	if !strings.Contains(string(managed), "Host *\n") {
		t.Fatal("Include did not reset host scope")
	}
	idBefore := cageStatus.Self.ID
	if _, err = n.run(ctx, request{Action: "disconnect"}); err != nil {
		t.Fatal(err)
	}
	saved, err := newNode(dir)
	if err != nil || len(saved.config.Keys) != 1 || saved.config.Enabled {
		t.Fatalf("disconnect lost data: %v %+v", err, saved)
	}
	if err = n.startServer(&tsnet.Server{Dir: dir, Hostname: "cage-test", ControlURL: server.URL, UserLogf: logger.Discard}); err != nil {
		t.Fatal(err)
	}
	restored, err := n.server.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Self.ID != idBefore {
		t.Fatal("reconnect replaced device identity")
	}
	t.Log("real two-node SSH passed in both directions; removed public key rejected; reconnect retained node identity and keys")
}

type bufferedConnection struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConnection) Read(b []byte) (int, error) { return c.reader.Read(b) }
func TestRejectPrivateKeysAndMultipleKeys(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	n, err := newNode(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"-----BEGIN OPENSSH PRIVATE KEY-----", "bad-key", "ssh-ed25519 AAAA\nssh-ed25519 AAAA"} {
		if _, err := n.run(context.Background(), request{Action: "add-key", Key: key}); err == nil {
			t.Fatal("invalid public key accepted")
		}
	}
}

func TestInteractiveAuthorizationFlow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TS_NO_LOGS_NO_SUPPORT", "true")
	netns.SetEnabled(false)
	defer netns.SetEnabled(true)
	control := &testcontrol.Server{RequireAuth: true, DERPMap: integration.RunDERPAndSTUN(t, logger.Discard, "127.0.0.1"), Logf: logger.Discard}
	server := httptest.NewUnstartedServer(control)
	control.HTTPTestServer = server
	server.Start()
	defer server.Close()
	n, err := newNode(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = n.startServer(&tsnet.Server{Dir: n.dir, Hostname: "cage-login-test", ControlURL: server.URL, UserLogf: logger.Discard}); err != nil {
		t.Fatal(err)
	}
	defer n.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err = n.run(ctx, request{Action: "connect"}); err != nil {
		t.Fatal(err)
	}
	var value state
	for ctx.Err() == nil {
		value, err = n.run(ctx, request{Action: "status"})
		if err == nil && value.URL != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if value.URL == "" {
		t.Fatal("interactive flow did not publish auth URL")
	}
	if !control.CompleteAuth(value.URL) {
		t.Fatal("auth flow not accepted")
	}
	if _, err = n.server.Up(ctx); err != nil {
		t.Fatal(err)
	}
	value, err = n.run(ctx, request{Action: "status"})
	if err != nil || value.Backend != "Running" {
		t.Fatalf("login not running: %+v %v", value, err)
	}
	// A broken optional SSH configuration must not hide a healthy private network.
	sshConfig := filepath.Join(os.Getenv("HOME"), ".ssh", "config")
	if err := os.Remove(sshConfig); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(sshConfig, 0700); err != nil {
		t.Fatal(err)
	}
	value, err = n.run(ctx, request{Action: "status"})
	if err != nil || value.Backend != "Running" || !strings.Contains(strings.Join(value.Health, "\n"), "SSH 自动配置未完成") {
		t.Fatalf("SSH configuration failure hid network status: %+v %v", value, err)
	}
}

func waitDERP(t *testing.T, ctx context.Context, s *tsnet.Server) {
	t.Helper()
	health := s.Sys().HealthTracker.Get()
	magic := s.Sys().MagicSock.Get()
	for {
		if report := magic.GetLastNetcheckReport(ctx); report != nil && report.PreferredDERP != 0 && !health.GetDERPRegionReceivedTime(report.PreferredDERP).IsZero() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}
