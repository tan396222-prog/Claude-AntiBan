package containerenv

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestGuestSSHNegotiatesPinnedHostKeyAndRejectsReplacement(t *testing.T) {
	r := newRuntime(t.TempDir(), fixtureConfig())
	if _, err := r.ensureSSHKey(); err != nil {
		t.Fatal(err)
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecdsaHost, err := ssh.NewSignerFromKey(other)
	if err != nil {
		t.Fatal(err)
	}
	known := fmt.Sprintf("cage-container-%s %s", r.identity, ssh.MarshalAuthorizedKey(pinned.PublicKey()))
	if err := os.WriteFile(filepath.Join(r.InstanceDir(), "known_hosts"), []byte(known), 0600); err != nil {
		t.Fatal(err)
	}
	configuration, err := r.guestSSHConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	for _, replacement := range []bool{false, true} {
		hostKey := pinned
		if replacement {
			_, private, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			hostKey, err = ssh.NewSignerFromKey(private)
			if err != nil {
				t.Fatal(err)
			}
		}
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		serverDone := make(chan struct{})
		go func() {
			defer close(serverDone)
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			defer connection.Close()
			_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
			serverConfig := &ssh.ServerConfig{NoClientAuth: true}
			serverConfig.AddHostKey(ecdsaHost)
			serverConfig.AddHostKey(hostKey)
			server, _, _, err := ssh.NewServerConn(connection, serverConfig)
			if err == nil {
				server.Close()
			}
		}()
		connection, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
		client, _, _, err := ssh.NewClientConn(connection, "cage-container-"+r.identity+":22", configuration)
		if replacement && err == nil {
			t.Error("replaced guest host key was accepted")
		}
		if !replacement && err != nil {
			t.Errorf("pinned ED25519 key was not negotiated alongside ECDSA: %v", err)
		}
		if client != nil {
			client.Close()
		}
		connection.Close()
		listener.Close()
		<-serverDone
	}
}
