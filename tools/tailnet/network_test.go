package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
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

func TestHTTPProxyUsesTailnetAndRevokesConnections(t *testing.T) {
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
	n, err := newNode(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = n.startServer(&tsnet.Server{Dir: n.dir, Hostname: "cage-proxy", ControlURL: server.URL, UserLogf: logger.Discard}); err != nil {
		t.Fatal(err)
	}
	defer func() { n.mu.Lock(); n.stop(); n.mu.Unlock() }()
	n.config.Enabled = true
	if _, err = n.server.Up(ctx); err != nil {
		t.Fatal(err)
	}
	remote := &tsnet.Server{Dir: t.TempDir(), Hostname: "private-web", ControlURL: server.URL, UserLogf: logger.Discard}
	defer remote.Close()
	peer, err := remote.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}
	waitDERP(t, ctx, n.server)
	waitDERP(t, ctx, remote)
	for {
		snapshot, err := n.run(ctx, request{Action: "status"})
		if err == nil && len(snapshot.Peers) > 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	listener, err := remote.Listen("tcp", ":8080")
	if err != nil {
		t.Fatal(err)
	}
	web := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("X-Remove") != "" {
			t.Error("proxy headers reached private service")
		}
		if r.URL.Path == "/stream" {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: live\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Tailnet-Service", "private-web")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, "%s %s %s", r.Method, r.URL.RequestURI(), body)
	})}
	go web.Serve(listener)
	defer web.Close()
	// Reuse a short-lived test certificate while carrying real TLS over tsnet.
	certificateServer := httptest.NewTLSServer(web.Handler)
	defer certificateServer.Close()
	tlsListener, err := remote.Listen("tcp", ":8443")
	if err != nil {
		t.Fatal(err)
	}
	secureWeb := &http.Server{Handler: web.Handler}
	go secureWeb.Serve(tls.NewListener(tlsListener, certificateServer.TLS))
	defer secureWeb.Close()
	proxyListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxyListener.Close()
	if err = n.serveDial(proxyListener); err != nil {
		t.Fatal(err)
	}
	var record dialRecord
	b, _ := os.ReadFile(filepath.Join(n.dir, "control.json"))
	if err = json.Unmarshal(b, &record); err != nil {
		t.Fatal(err)
	}
	proxyURL := &url.URL{Scheme: "http", Host: record.Address, User: url.UserPassword("cage", record.Token)}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true, TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	for _, host := range []string{peer.TailscaleIPs[0].String(), "private-web.cage-test.ts.net", "private-web"} {
		r, _ := http.NewRequestWithContext(ctx, "POST", "http://"+host+":8080/hello?q=world", strings.NewReader("payload"))
		r.Header.Set("Connection", "X-Remove")
		r.Header.Set("X-Remove", "secret")
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusCreated || string(body) != "POST /hello?q=world payload" || response.Header.Get("X-Tailnet-Service") != "private-web" {
			t.Fatalf("proxy response: %d %q", response.StatusCode, body)
		}
	}
	secureResponse, err := client.Get("https://private-web:8443/encrypted")
	if err != nil {
		t.Fatal(err)
	}
	secureBody, _ := io.ReadAll(secureResponse.Body)
	secureResponse.Body.Close()
	if secureResponse.TLS == nil || string(secureBody) != "GET /encrypted " {
		t.Fatalf("HTTPS did not traverse tailnet: %q", secureBody)
	}
	stream, err := client.Get("http://private-web:8080/stream")
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stream.Body).ReadString('\n')
	stream.Body.Close()
	if err != nil || line != "data: live\n" {
		t.Fatalf("stream buffered: %q %v", line, err)
	}
	for _, target := range []string{"http://127.0.0.1:8080/", "http://1.1.1.1/", "http://192.168.1.1/", "http://unknown.cage-test.ts.net/"} {
		response, err := client.Get(target)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusBadGateway {
			t.Fatalf("non-peer %s accepted: %d", target, response.StatusCode)
		}
	}
	unauthTransport := &http.Transport{Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: record.Address})}
	defer unauthTransport.CloseIdleConnections()
	unauth := &http.Client{Transport: unauthTransport, Timeout: 5 * time.Second}
	response, err := unauth.Get("http://private-web:8080/")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("missing auth accepted: %d", response.StatusCode)
	}
	open := func(host string, port int) (string, error) {
		n.mu.Lock()
		defer n.mu.Unlock()
		return n.openForward(ctx, host, port)
	}
	for _, invalid := range []struct {
		host string
		port int
	}{{"private-web", 0}, {"private-web", 65536}, {"127.0.0.1", 8080}, {"1.1.1.1", 443}, {"unknown", 8080}} {
		if address, err := open(invalid.host, invalid.port); err == nil {
			t.Fatalf("invalid forward accepted: %+v %s", invalid, address)
		}
	}
	address, err := open("private-web", 8080)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := open(peer.TailscaleIPs[0].String(), 8080); err != nil || again != address {
		t.Fatalf("duplicate mapping not reused: %s %s %v", address, again, err)
	}
	direct := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second}
	localResponse, err := direct.Get("http://" + address + "/local-service")
	if err != nil {
		t.Fatal(err)
	}
	localBody, _ := io.ReadAll(localResponse.Body)
	localResponse.Body.Close()
	if string(localBody) != "GET /local-service " {
		t.Fatalf("mapped HTTP: %q", localBody)
	}
	echoListener, err := remote.Listen("tcp", ":9001")
	if err != nil {
		t.Fatal(err)
	}
	defer echoListener.Close()
	go func() {
		for {
			c, e := echoListener.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	echoAddress, err := open("private-web", 9001)
	if err != nil {
		t.Fatal(err)
	}
	echo, err := net.DialTimeout("tcp", echoAddress, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	echo.SetDeadline(time.Now().Add(10 * time.Second))
	_, _ = echo.Write([]byte("arbitrary TCP payload"))
	echoPayload := make([]byte, len("arbitrary TCP payload"))
	if _, err = io.ReadFull(echo, echoPayload); err != nil || string(echoPayload) != "arbitrary TCP payload" {
		t.Fatalf("mapped TCP: %q %v", echoPayload, err)
	}
	half, err := net.DialTimeout("tcp", echoAddress, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	half.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = half.Write([]byte("reply after FIN"))
	_ = half.(*net.TCPConn).CloseWrite()
	halfReply, err := io.ReadAll(half)
	half.Close()
	if err != nil || string(halfReply) != "reply after FIN" {
		t.Fatalf("half-closed TCP reply: %q %v", halfReply, err)
	}
	conn, err := net.DialTimeout("tcp", record.Address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	authorization := base64.StdEncoding.EncodeToString([]byte("cage:" + record.Token))
	fmt.Fprintf(conn, "CONNECT private-web:8080 HTTP/1.1\r\nHost: private-web:8080\r\nProxy-Authorization: Basic %s\r\n\r\n", authorization)
	reader := bufio.NewReader(conn)
	connected, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
	if err != nil || connected.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT: %v %v", connected, err)
	}
	fmt.Fprint(conn, "GET /tunnel HTTP/1.1\r\nHost: private-web:8080\r\n\r\n")
	response, err = http.ReadResponse(reader, &http.Request{Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != "GET /tunnel " {
		t.Fatalf("tunnel payload: %q", body)
	}
	if _, err = n.run(ctx, request{Action: "disconnect"}); err != nil {
		t.Fatal(err)
	}
	if _, err = reader.ReadByte(); err == nil {
		t.Fatal("disconnect left tunnel alive")
	}
	if _, err = echo.Read(make([]byte, 1)); err == nil {
		t.Fatal("disconnect left mapped TCP alive")
	}
	if c, err := net.DialTimeout("tcp", address, time.Second); err == nil {
		c.Close()
		t.Fatal("disconnect left mapping listener alive")
	}
	n.mu.Lock()
	remaining := len(n.forwards)
	n.mu.Unlock()
	if remaining != 0 {
		t.Fatal("disconnect retained old mapping")
	}
	response, err = client.Get("http://private-web:8080/")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("disconnected proxy dialed: %d", response.StatusCode)
	}
}
