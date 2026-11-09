package remotehost

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

type sshFixture struct {
	profile      HostProfile
	manifest     ServerManifest
	commands     chan string
	connections  sync.Map
	listener     net.Listener
	forwardCount atomic.Int64
}

func newFixture(t *testing.T, handler http.Handler, extraHostKeys ...ssh.Signer) *sshFixture {
	t.Helper()
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	_, hostPrivate, _ := ed25519.GenerateKey(rand.Reader)
	hostSigner, _ := ssh.NewSignerFromKey(hostPrivate)
	_, clientPrivate, _ := ed25519.GenerateKey(rand.Reader)
	clientSigner, _ := ssh.NewSignerFromKey(clientPrivate)
	keyBytes, _ := x509.MarshalPKCS8PrivateKey(clientPrivate)
	keyPath := filepath.Join(t.TempDir(), "identity")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes}), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &sshFixture{listener: listener, commands: make(chan string, 8), manifest: ServerManifest{APIProtocol: 1, InstanceID: strings.Repeat("a", 64), Version: "test", Platform: "linux", Runtime: "container", Origin: httpServer.URL, Ticket: strings.Repeat("t", 64), Capabilities: map[string]bool{"terminal": true}}}
	f.profile = HostProfile{ID: newID(), Name: "fixture", Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, User: "cage", Auth: AuthConfig{Kind: "key_file", IdentityFile: keyPath}, HostKey: &HostKey{Algorithm: hostSigner.PublicKey().Type(), Fingerprint: ssh.FingerprintSHA256(hostSigner.PublicKey())}}
	config := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if string(key.Marshal()) != string(clientSigner.PublicKey().Marshal()) {
			return nil, errors.New("wrong key")
		}
		return nil, nil
	}}
	config.AddHostKey(hostSigner)
	for _, key := range extraHostKeys {
		config.AddHostKey(key)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			f.connections.Store(conn, true)
			go func() {
				defer f.connections.Delete(conn)
				defer conn.Close()
				server, channels, requests, err := ssh.NewServerConn(conn, config)
				if err != nil {
					return
				}
				defer server.Close()
				go ssh.DiscardRequests(requests)
				for channel := range channels {
					go f.channel(channel)
				}
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		f.connections.Range(func(key, value any) bool { key.(net.Conn).Close(); return true })
	})
	return f
}

func (f *sshFixture) channel(in ssh.NewChannel) {
	switch in.ChannelType() {
	case "session":
		channel, requests, err := in.Accept()
		if err != nil {
			return
		}
		defer channel.Close()
		for request := range requests {
			if request.Type != "exec" {
				request.Reply(false, nil)
				continue
			}
			var command struct{ Command string }
			ssh.Unmarshal(request.Payload, &command)
			f.commands <- command.Command
			request.Reply(true, nil)
			json.NewEncoder(channel).Encode(f.manifest)
			channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{0}))
			return
		}
	case "direct-tcpip":
		var forward struct {
			Host       string
			Port       uint32
			Origin     string
			OriginPort uint32
		}
		if ssh.Unmarshal(in.ExtraData(), &forward) != nil {
			in.Reject(ssh.Prohibited, "invalid")
			return
		}
		u, _ := url.Parse(f.manifest.Origin)
		port, _ := strconv.Atoi(u.Port())
		if forward.Host != "127.0.0.1" || int(forward.Port) != port {
			in.Reject(ssh.Prohibited, "wrong destination")
			return
		}
		peer, err := net.Dial("tcp", net.JoinHostPort(forward.Host, strconv.Itoa(int(forward.Port))))
		if err != nil {
			in.Reject(ssh.ConnectionFailed, "dial")
			return
		}
		defer peer.Close()
		channel, requests, err := in.Accept()
		if err != nil {
			return
		}
		defer channel.Close()
		f.forwardCount.Add(1)
		go ssh.DiscardRequests(requests)
		done := make(chan struct{})
		go func() { io.Copy(peer, channel); peer.Close(); close(done) }()
		io.Copy(channel, peer)
		channel.Close()
		<-done
	default:
		in.Reject(ssh.UnknownChannelType, "unsupported")
	}
}

func fixtureHandler(t *testing.T, next http.Handler) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "http://"+r.Host {
			http.Error(w, "origin", 403)
			return
		}
		if r.URL.Path == "/api/session" {
			var body struct{ Token string }
			json.NewDecoder(r.Body).Decode(&body)
			if body.Token != strings.Repeat("t", 64) {
				http.Error(w, "ticket", 401)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "remote_session", Value: "private-cookie", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			io.WriteString(w, `{"ok":true}`)
			return
		}
		cookie, err := r.Cookie("remote_session")
		if err != nil || cookie.Value != "private-cookie" || r.Header.Get("Authorization") != "" || strings.Contains(r.Header.Get("Cookie"), "local-secret") {
			http.Error(w, "auth", 401)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func connectFixture(t *testing.T, f *sshFixture) *Session {
	t.Helper()
	s, err := connect(context.Background(), f.profile, ConnectSecrets{}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSSHSessionHTTPWebSocketSSEAndCancellation(t *testing.T) {
	sseEnded := make(chan struct{})
	f := newFixture(t, fixtureHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/check":
			io.WriteString(w, "authenticated")
		case "/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: ready\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			close(sseEnded)
		case "/ws":
			connection, err := (&websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "http://"+r.Host }}).Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer connection.Close()
			kind, data, err := connection.ReadMessage()
			if err == nil {
				connection.WriteMessage(kind, data)
			}
		default:
			http.NotFound(w, r)
		}
	})))
	s := connectFixture(t, f)
	if command := <-f.commands; command != "exec 'cage' server-entry --json" {
		t.Fatalf("command = %q", command)
	}
	req, _ := http.NewRequest("GET", s.Origin+"/check", nil)
	req.Header.Set("Cookie", "local=local-secret")
	req.Header.Set("Authorization", "Bearer local-secret")
	response, err := s.Client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != "authenticated" {
		t.Fatalf("HTTP = %d %q", response.StatusCode, body)
	}
	u, _ := url.Parse(s.Origin)
	proxy := httputil.NewSingleHostReverseProxy(u)
	proxy.Transport = s.Transport
	gateway := httptest.NewServer(proxy)
	defer gateway.Close()
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(gateway.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if err = ws.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	_, message, err := ws.ReadMessage()
	if err != nil || string(message) != "hello" {
		t.Fatalf("websocket: %q %v", message, err)
	}
	stream, err := s.Client.Get(s.Origin + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	first := make([]byte, len("data: ready\n\n"))
	if _, err = io.ReadFull(stream.Body, first); err != nil {
		t.Fatal(err)
	}
	s.Close()
	stream.Body.Close()
	select {
	case <-sseEnded:
	case <-time.After(3 * time.Second):
		t.Fatal("SSE remained open after disconnect")
	}
	if _, err = s.Client.Get(s.Origin + "/check"); err == nil {
		t.Fatal("closed session accepted request")
	}
}

func TestHostKeyRequiresExplicitMatchingPin(t *testing.T) {
	f := newFixture(t, fixtureHandler(t, http.NotFoundHandler()))
	for _, code := range []string{"host_key_unknown", "host_key_changed"} {
		p := f.profile
		if code == "host_key_unknown" {
			p.HostKey = nil
		} else {
			p.HostKey = &HostKey{Algorithm: p.HostKey.Algorithm, Fingerprint: "SHA256:" + strings.Repeat("A", 43)}
		}
		_, err := connect(context.Background(), p, ConnectSecrets{}, func(string) {})
		var structured *Error
		if !errors.As(err, &structured) || structured.Code != code || structured.HostKey == nil || structured.HostKey.Fingerprint != f.profile.HostKey.Fingerprint {
			t.Fatalf("%s: %#v", code, err)
		}
	}
	if f.forwardCount.Load() != 0 {
		t.Fatal("untrusted host reached HTTP service")
	}
}

func TestPinnedHostKeySelectsMatchingAlgorithmOnMultiKeyServer(t *testing.T) {
	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecdsaSigner, err := ssh.NewSignerFromKey(ecdsaKey)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rsaSigner, err := ssh.NewSignerFromKey(rsaKey)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, fixtureHandler(t, http.NotFoundHandler()), ecdsaSigner, rsaSigner)
	// The stored ED25519 key must win even if the client's default preference
	// would negotiate the server's ECDSA key first.
	connectFixture(t, f).Close()
	// RSA's public key type differs from its SHA-2 signature algorithm names.
	f.profile.HostKey = &HostKey{Algorithm: ssh.KeyAlgoRSA, Fingerprint: ssh.FingerprintSHA256(rsaSigner.PublicKey())}
	connectFixture(t, f).Close()
}

func TestOriginRedirectAndMutationReplayRefused(t *testing.T) {
	var mutations atomic.Int64
	f := newFixture(t, fixtureHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "http://public.example/private", 302)
			return
		}
		if r.URL.Path == "/mutation" {
			mutations.Add(1)
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		io.WriteString(w, "ok")
	})))
	s := connectFixture(t, f)
	if _, err := s.Client.Get("http://public.example/check"); err == nil {
		t.Fatal("arbitrary destination accepted")
	}
	if _, err := s.Client.Get(s.Origin + "/redirect"); err == nil {
		t.Fatal("redirect accepted")
	}
	req, _ := http.NewRequest("POST", s.Origin+"/mutation", strings.NewReader("operation"))
	req.Header.Set("Idempotency-Key", "do-not-replay")
	if _, err := s.Client.Do(req); err == nil {
		t.Fatal("missing mutation response accepted")
	}
	if mutations.Load() != 1 {
		t.Fatalf("mutation count = %d", mutations.Load())
	}
}

func TestServerEntryCommandQuotesPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX server command is executed by the remote Linux shell")
	}
	dir := t.TempDir()
	executable := filepath.Join(dir, "cage ' literal")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf '%s\\n%s\\n%s\\n' \"$CLAUDE_CAGE_CONFIG\" \"$1\" \"$2\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	p := HostProfile{Name: "test", Host: "localhost", User: "user", Auth: AuthConfig{Kind: "agent"}, Executable: executable, ConfigPath: filepath.Join(dir, "a'$(touch injected).json")}
	command, err := ServerEntryCommand(p)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != p.ConfigPath+"\nserver-entry\n--json\n" {
		t.Fatalf("command output = %q", output)
	}
	if _, err = os.Stat(filepath.Join(dir, "injected")); !os.IsNotExist(err) {
		t.Fatal("shell substitution executed")
	}
}

func TestManifestRejectsRemoteTargets(t *testing.T) {
	manifest := ServerManifest{APIProtocol: 1, InstanceID: strings.Repeat("a", 64), Platform: "linux", Runtime: "container", Origin: "http://localhost:17800", Ticket: strings.Repeat("t", 64)}
	for _, origin := range []string{"https://localhost:17800", "http://example.com:80", "http://127.0.0.1:17800/path", "http://user@localhost:17800", "http://localhost:17800?x=1", "http://localhost:17800#x", "http://localhost"} {
		manifest.Origin = origin
		if ValidateManifest(manifest) == nil {
			t.Errorf("accepted %s", origin)
		}
	}
	manifest.Origin = "http://localhost:17800"
	if err := ValidateManifest(manifest); err != nil {
		t.Fatal(err)
	}
	manifest.APIProtocol = 2
	if err := ValidateManifest(manifest); err == nil {
		t.Fatal("accepted unknown protocol")
	}
	manifest.APIProtocol = APIProtocol
	manifest.InstanceID = newID()
	if err := ValidateManifest(manifest); err == nil {
		t.Fatal("accepted an instance identity that cannot be used by file and account consumers")
	}
}

func waitState(t *testing.T, m *Manager, want string) ConnectionState {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state := m.Snapshot()
		if state.State == want {
			return state
		}
		if state.State == "failed" && want != "failed" {
			t.Fatalf("connection failed: %+v", state)
		}
		time.Sleep(time.Millisecond * 5)
	}
	t.Fatalf("state never became %s: %+v", want, m.Snapshot())
	return ConnectionState{}
}

func TestManagerGenerationAndDisconnectDoNotStopServer(t *testing.T) {
	var stopCalls atomic.Int64
	f := newFixture(t, fixtureHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "stop") || strings.Contains(r.URL.Path, "suspend") {
			stopCalls.Add(1)
		}
		io.WriteString(w, "ok")
	})))
	store := NewStore(t.TempDir())
	p := f.profile
	p.ID = ""
	p, err := store.Upsert(p)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(store)
	defer m.Close()
	first, err := m.Connect(p.ID, ConnectSecrets{})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "connected")
	old, err := m.Acquire(first.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Disconnect(first.ConnectionID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-old.Context.Done():
	case <-time.After(time.Second):
		t.Fatal("connection not cancelled")
	}
	second, err := m.Connect(p.ID, ConnectSecrets{})
	if err != nil {
		t.Fatal(err)
	}
	state := waitState(t, m, "connected")
	if second.ConnectionID == first.ConnectionID || state.InstanceID != f.manifest.InstanceID {
		t.Fatal("incorrect identity lifecycle")
	}
	if _, err = m.Acquire(first.ConnectionID); err == nil {
		t.Fatal("obsolete connection acquired")
	}
	if err = m.Disconnect(first.ConnectionID); err == nil {
		t.Fatal("old generation disconnected new connection")
	}
	if _, err = m.Acquire(second.ConnectionID); err != nil {
		t.Fatal(err)
	}
	if stopCalls.Load() != 0 {
		t.Fatal("disconnect stopped remote environment")
	}
	encoded, _ := json.Marshal(state)
	if strings.Contains(string(encoded), strings.Repeat("t", 64)) || strings.Contains(string(encoded), "private-cookie") {
		t.Fatal("connection secrets leaked into state")
	}
	f.connections.Range(func(key, value any) bool { key.(net.Conn).Close(); return true })
	waitState(t, m, "failed")
}

func TestExpiredHTTPServerSessionInvalidatesLiveSSHConnectionWithoutReplay(t *testing.T) {
	var expired atomic.Bool
	var requests atomic.Int64
	f := newFixture(t, fixtureHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if expired.Load() {
			http.Error(w, "expired server cookie", http.StatusUnauthorized)
			return
		}
		io.WriteString(w, "ok")
	})))
	store := NewStore(t.TempDir())
	p := f.profile
	p.ID = ""
	p, err := store.Upsert(p)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(store)
	defer manager.Close()
	state, err := manager.Connect(p.ID, ConnectSecrets{})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, manager, "connected")
	session, err := manager.Acquire(state.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	response, err := session.Client.Get(session.Origin + "/ready")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if session.Context.Err() != nil {
		t.Fatal("SSH session was already closed before server expiry")
	}
	expired.Store(true)
	before := requests.Load()
	request, _ := http.NewRequest("POST", session.Origin+"/mutation", strings.NewReader(`{"operation":"start"}`))
	_, err = session.Client.Do(request)
	var structured *Error
	if !errors.As(err, &structured) || structured.Code != "remote_session_expired" {
		t.Fatalf("session expiry = %v", err)
	}
	failed := waitState(t, manager, "failed")
	if failed.Message != "远程连接已中断，请重新连接并核对云端任务" {
		t.Fatalf("misleading recovery message: %s", failed.Message)
	}
	if requests.Load() != before+1 {
		t.Fatal("request was replayed after server session expiry")
	}
	if _, err := manager.Acquire(state.ConnectionID); err == nil {
		t.Fatal("expired session remained available")
	}
}

func TestStorePreservesIndependentUpdatesAndRejectsUnsafeFiles(t *testing.T) {
	dir := t.TempDir()
	var group sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			_, err := NewStore(dir).Upsert(HostProfile{Name: fmt.Sprint(i), Host: "localhost", User: "cage", Auth: AuthConfig{Kind: "agent"}})
			errs <- err
		}(i)
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	profiles, err := NewStore(dir).List()
	if err != nil || len(profiles) != 12 {
		t.Fatalf("profiles=%d err=%v", len(profiles), err)
	}
	if err = NewStore(dir).Delete(profiles[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = NewStore(dir).Get(profiles[0].ID); err == nil {
		t.Fatal("deleted host retained")
	}
	if runtime.GOOS != "windows" {
		if err = os.Chmod(filepath.Join(dir, "remote-hosts.json"), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err = NewStore(dir).List(); err == nil {
			t.Fatal("world-readable configuration accepted")
		}
	}
	other := t.TempDir()
	if err = os.Symlink(filepath.Join(dir, "remote-hosts.json"), filepath.Join(other, "remote-hosts.json")); err == nil {
		if _, err = NewStore(other).List(); err == nil {
			t.Fatal("symlink config accepted")
		}
	}
}
