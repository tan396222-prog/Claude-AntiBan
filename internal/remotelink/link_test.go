package remotelink

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type handshakeResult struct {
	connection *tls.Conn
	err        error
}

func identityForTest(t *testing.T) *Identity {
	t.Helper()
	identity, err := NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func socketsForTest(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		accepted <- ws
	}))
	t.Cleanup(server.Close)
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	peer := <-accepted
	t.Cleanup(func() { _ = client.Close(); _ = peer.Close() })
	return client, peer
}

func connectForTest(t *testing.T, ctx context.Context) (*tls.Conn, *tls.Conn, *websocket.Conn) {
	t.Helper()
	clientIdentity, serverIdentity := identityForTest(t), identityForTest(t)
	clientWS, serverWS := socketsForTest(t)
	result := make(chan handshakeResult, 1)
	go func() {
		connection, err := Server(ctx, serverWS, serverIdentity, clientIdentity.Pin)
		result <- handshakeResult{connection, err}
	}()
	client, err := Client(ctx, clientWS, clientIdentity, serverIdentity.Pin)
	if err != nil {
		t.Fatal(err)
	}
	remote := <-result
	if remote.err != nil {
		t.Fatal(remote.err)
	}
	return client, remote.connection, serverWS
}

func TestMutualTLSAndBidirectionalStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, server, _ := connectForTest(t, ctx)
	for _, connection := range []*tls.Conn{client, server} {
		state := connection.ConnectionState()
		if state.Version != tls.VersionTLS13 || state.NegotiatedProtocol != Protocol || len(state.PeerCertificates) != 1 {
			t.Fatalf("unexpected authenticated connection: version=%x protocol=%q peers=%d", state.Version, state.NegotiatedProtocol, len(state.PeerCertificates))
		}
		transport := connection.NetConn().(*webSocketConn)
		if !transport.writeDeadline.IsZero() {
			t.Fatal("handshake write deadline was not cleared")
		}
		if err := connection.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	// A completed connection must survive cancellation of its handshake context.
	cancel()
	payload := make([]byte, 256<<10)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	reply := []byte("手机已连接，回复不依赖 WebSocket 消息边界。")
	done := make(chan error, 1)
	go func() {
		received := make([]byte, len(payload))
		if _, err := io.ReadFull(server, received); err != nil {
			done <- err
			return
		}
		if !bytes.Equal(payload, received) {
			done <- errors.New("server received altered plaintext")
			return
		}
		_, err := server.Write(reply)
		done <- err
	}()
	if _, err := client.Write(payload); err != nil {
		t.Fatal(err)
	}
	received := make([]byte, len(reply))
	if _, err := io.ReadFull(client, received); err != nil || !bytes.Equal(received, reply) {
		t.Fatalf("client reply: %q, %v", received, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRejectWrongPeerPins(t *testing.T) {
	for _, side := range []string{"client", "server"} {
		t.Run(side, func(t *testing.T) {
			clientIdentity, serverIdentity, impostor := identityForTest(t), identityForTest(t), identityForTest(t)
			clientWS, serverWS := socketsForTest(t)
			clientPin, serverPin := clientIdentity.Pin, serverIdentity.Pin
			if side == "client" {
				serverPin = impostor.Pin
			} else {
				clientPin = impostor.Pin
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := Server(ctx, serverWS, serverIdentity, clientPin); result <- err }()
			client, clientErr := Client(ctx, clientWS, clientIdentity, serverPin)
			serverErr := <-result
			if side == "client" && (clientErr == nil || !strings.Contains(clientErr.Error(), "paired public key")) {
				t.Fatalf("client accepted wrong server pin: %v", clientErr)
			}
			if side == "server" && (serverErr == nil || !strings.Contains(serverErr.Error(), "paired public key")) {
				t.Fatalf("server accepted wrong client pin: %v", serverErr)
			}
			if client != nil {
				_ = client.NetConn().Close()
			}
		})
	}
}

func TestRejectEmptyOrMalformedPins(t *testing.T) {
	identity := identityForTest(t)
	for _, connect := range []struct {
		name string
		fn   func(context.Context, *websocket.Conn, *Identity, string) (*tls.Conn, error)
	}{{"client", Client}, {"server", Server}} {
		for _, pin := range []string{"", "sha256:", "sha256:not-base64", strings.TrimSuffix(identity.Pin, "=")} {
			t.Run(connect.name+"/"+pin, func(t *testing.T) {
				ws, _ := socketsForTest(t)
				if _, err := connect.fn(context.Background(), ws, identity, pin); err == nil {
					t.Fatal("accepted missing or malformed pin")
				}
			})
		}
	}
}

func TestRejectExpiredPinnedCertificate(t *testing.T) {
	clientIdentity, serverIdentity := identityForTest(t), identityForTest(t)
	private := serverIdentity.Certificate.PrivateKey.(ed25519.PrivateKey)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-2 * time.Hour), NotAfter: time.Now().Add(-time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, private.Public(), private)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	serverIdentity.Certificate.Certificate = [][]byte{der}
	serverIdentity.Certificate.Leaf = leaf
	digest := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	if serverIdentity.Pin != "sha256:"+base64.StdEncoding.EncodeToString(digest[:]) {
		t.Fatal("test changed the pinned public key")
	}
	clientWS, serverWS := socketsForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := Server(ctx, serverWS, serverIdentity, clientIdentity.Pin); result <- err }()
	_, err = Client(ctx, clientWS, clientIdentity, serverIdentity.Pin)
	if err == nil || !strings.Contains(err.Error(), "validity period") {
		t.Fatalf("expired pinned certificate accepted: %v", err)
	}
	<-result
}

func TestRejectTamperedTLSRecord(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, _, serverWS := connectForTest(t, ctx)
	if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	// An untrusted forwarding relay can inject a binary frame but cannot create
	// a TLS record with a valid authentication tag after the handshake.
	forged := append([]byte{23, 3, 3, 0, 17}, make([]byte, 17)...)
	if err := serverWS.WriteMessage(websocket.BinaryMessage, forged); err != nil {
		t.Fatal(err)
	}
	var output [1]byte
	if n, err := client.Read(output[:]); n != 0 || err == nil || !strings.Contains(err.Error(), "bad record MAC") {
		t.Fatalf("tampering result: bytes=%d error=%v", n, err)
	}
}

func TestCancelHandshake(t *testing.T) {
	clientIdentity, serverIdentity := identityForTest(t), identityForTest(t)
	clientWS, silentPeer := socketsForTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := Client(ctx, clientWS, clientIdentity, serverIdentity.Pin); result <- err }()
	if err := silentPeer.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := silentPeer.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled handshake: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not unblock handshake")
	}
}

func TestRejectTextAndOversizedMessages(t *testing.T) {
	for _, test := range []struct {
		name string
		kind int
		data []byte
	}{
		{"text", websocket.TextMessage, []byte("not a binary transport")},
		{"oversized", websocket.BinaryMessage, make([]byte, MaxMessageSize+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			local, peer := socketsForTest(t)
			stream := newWebSocketConn(local)
			_ = stream.SetReadDeadline(time.Now().Add(2 * time.Second))
			done := make(chan error, 1)
			go func() { done <- peer.WriteMessage(test.kind, test.data) }()
			_, err := io.Copy(io.Discard, stream)
			if err == nil {
				t.Fatal("accepted invalid WebSocket message")
			}
			if test.name == "text" && !strings.Contains(err.Error(), "only binary") {
				t.Fatalf("wrong text rejection: %v", err)
			}
			if test.name == "oversized" && !errors.Is(err, websocket.ErrReadLimit) {
				t.Fatalf("wrong size rejection: %v", err)
			}
			<-done
		})
	}
}

func TestWriteFragmentsAndReadAcrossBoundaries(t *testing.T) {
	local, peer := socketsForTest(t)
	stream := newWebSocketConn(local)
	payload := bytes.Repeat([]byte("binary-stream-"), 8000)
	done := make(chan error, 1)
	go func() { _, err := stream.Write(payload); done <- err }()
	var received []byte
	for len(received) < len(payload) {
		kind, message, err := peer.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if kind != websocket.BinaryMessage || len(message) > writeMessageSize {
			t.Fatalf("invalid write frame: type=%d size=%d", kind, len(message))
		}
		received = append(received, message...)
	}
	if err := <-done; err != nil || !bytes.Equal(received, payload) {
		t.Fatalf("fragmented write changed bytes: %v", err)
	}
	go func() {
		for _, part := range [][]byte{nil, []byte("ab"), []byte("c"), []byte("def")} {
			if err := peer.WriteMessage(websocket.BinaryMessage, part); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	var joined [6]byte
	if _, err := io.ReadFull(stream, joined[:]); err != nil || string(joined[:]) != "abcdef" {
		t.Fatalf("message boundaries leaked into stream: %q %v", joined, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
