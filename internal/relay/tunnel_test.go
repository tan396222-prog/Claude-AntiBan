package relay

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/sylvinus/agent-vm/internal/remotelink"
)

// This exercises the real relay and peer transport together. It deliberately
// does not claim device pairing, application RPC, or a mobile app is present.
func TestMutualTLSAcrossRelay(t *testing.T) {
	service, server := testServer(t, Config{})
	i := createRoom(t, server.URL, testAdmin, http.StatusCreated)
	hostWS := dial(t, server.URL, i, "host", i.HostToken, http.StatusSwitchingProtocols)
	clientWS := dial(t, server.URL, i, "client", i.ClientToken, http.StatusSwitchingProtocols)
	hostIdentity, err := remotelink.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	clientIdentity, err := remotelink.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type result struct {
		connection *tls.Conn
		err        error
	}
	hostResult := make(chan result, 1)
	go func() {
		connection, err := remotelink.Server(ctx, hostWS, hostIdentity, clientIdentity.Pin)
		hostResult <- result{connection, err}
	}()
	client, err := remotelink.Client(ctx, clientWS, clientIdentity, hostIdentity.Pin)
	if err != nil {
		t.Fatal(err)
	}
	host := <-hostResult
	if host.err != nil {
		t.Fatal(host.err)
	}
	defer host.connection.Close()
	defer client.Close()
	_ = host.connection.SetDeadline(time.Now().Add(3 * time.Second))
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	payload := bytes.Repeat([]byte("Cage 远程传输 — binary\x00\xff\n"), 12000)
	writeDone := make(chan error, 1)
	go func() { _, err := client.Write(payload); writeDone <- err }()
	received := make([]byte, len(payload))
	if _, err := io.ReadFull(host.connection, received); err != nil {
		t.Fatal(err)
	}
	if err := <-writeDone; err != nil || !bytes.Equal(received, payload) {
		t.Fatalf("large encrypted transfer failed: %v", err)
	}
	reply := []byte("主机已收到完整内容")
	go func() { _, err := host.connection.Write(reply); writeDone <- err }()
	if _, err := io.ReadFull(client, received[:len(reply)]); err != nil {
		t.Fatal(err)
	}
	if err := <-writeDone; err != nil || !bytes.Equal(received[:len(reply)], reply) {
		t.Fatalf("encrypted reply failed: %v", err)
	}
	_ = service.Close()
	if _, err := client.Read(received[:1]); err == nil {
		t.Fatal("relay shutdown did not close encrypted client stream")
	}
}
