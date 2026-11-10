package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sylvinus/agent-vm/internal/relay"
)

func TestTokenFileValidation(t *testing.T) {
	secret := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name    string
		content string
		valid   bool
	}{
		{"generated", secret + "\n", true},
		{"windows-newline", secret + "\r\n", true},
		{"bare-carriage-return", secret + "\r", false},
		{"no-newline", secret, true},
		{"too-short", strings.Repeat("a", 31), false},
		{"too-large", strings.Repeat("a", maxTokenBytes+1), false},
		{"embedded-newline", secret + "\nignored", false},
		{"space", " " + secret, false},
		{"control", secret + "\x00", false},
		{"non-ascii", secret + "密钥", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "token")
			if err := os.WriteFile(name, []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := readTokenFile(name)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
			if tc.valid && got != secret {
				t.Fatal("token changed during parsing")
			}
			if err != nil && strings.Contains(err.Error(), secret) {
				t.Fatal("validation error disclosed the token")
			}
		})
	}
	if _, err := readTokenFile(""); err == nil {
		t.Fatal("missing token file accepted")
	}
	if _, err := readTokenFile(t.TempDir()); err == nil {
		t.Fatal("directory accepted as token file")
	}
}

func TestTokenFileRejectsSharedUnixPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses ACLs")
	}
	name := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(name, []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0640, 0604, 0620, 0601} {
		if err := os.Chmod(name, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := readTokenFile(name); err == nil {
			t.Fatalf("token with mode %04o accepted", mode)
		}
	}
}

func TestRunRejectsMissingTokenAndInvalidLimits(t *testing.T) {
	for _, args := range [][]string{nil, {"--max-rooms=0"}, {"--pair-timeout=0s"}, {"--idle-timeout=-1s"}, {"unexpected"}} {
		if err := run(context.Background(), args, io.Discard); err == nil {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
}

func TestShutdownClosesPairedWebSockets(t *testing.T) {
	secret := strings.Repeat("x", 64)
	server, err := relay.New(relay.Config{AdminToken: secret})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer server.Close()
	defer listener.Close()
	done := make(chan error, 1)
	go func() { done <- serve(ctx, listener, server, log.New(io.Discard, "", 0)) }()
	base := listener.Addr().String()
	request, err := http.NewRequest(http.MethodPost, "http://"+base+"/v1/rooms", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+secret)
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var invitation relay.Invitation
	err = json.NewDecoder(response.Body).Decode(&invitation)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create room status=%d", response.StatusCode)
	}
	dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second}
	connect := func(role, token string) *websocket.Conn {
		t.Helper()
		header := http.Header{"Authorization": []string{"Bearer " + token}}
		connection, _, err := dialer.Dial("ws://"+base+"/v1/rooms/"+invitation.ID+"/"+role, header)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { connection.Close() })
		return connection
	}
	host := connect("host", invitation.HostToken)
	peer := connect("client", invitation.ClientToken)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not complete")
	}
	for _, connection := range []*websocket.Conn{host, peer} {
		_ = connection.SetReadDeadline(time.Now().Add(time.Second))
		_, _, err := connection.ReadMessage()
		if err == nil {
			t.Fatal("WebSocket stayed usable after shutdown")
		}
		if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
			t.Fatal("WebSocket was not closed by shutdown")
		}
	}
}
