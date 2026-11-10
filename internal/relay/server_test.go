package relay

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const testAdmin = "relay-test-admin-token-not-a-real-credential"

func testServer(t *testing.T, config Config) (*Server, *httptest.Server) {
	t.Helper()
	config.AdminToken = testAdmin
	s, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(s)
	t.Cleanup(func() { _ = s.Close(); httpServer.Close() })
	return s, httpServer
}

func createRoom(t *testing.T, serverURL, token string, expectedStatus int) Invitation {
	t.Helper()
	r, err := http.NewRequest(http.MethodPost, serverURL+"/v1/rooms", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != expectedStatus {
		t.Fatalf("create status = %d, want %d", response.StatusCode, expectedStatus)
	}
	var invitation Invitation
	if expectedStatus == http.StatusCreated {
		if err := json.NewDecoder(response.Body).Decode(&invitation); err != nil {
			t.Fatal(err)
		}
	}
	return invitation
}

func dial(t *testing.T, serverURL string, invitation Invitation, role, token string, expectedStatus int) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(serverURL, "http") + "/v1/rooms/" + invitation.ID + "/" + role
	connection, response, err := websocket.DefaultDialer.Dial(url, http.Header{"Authorization": {"Bearer " + token}})
	if expectedStatus == http.StatusSwitchingProtocols {
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		t.Cleanup(func() { _ = connection.Close() })
		return connection
	}
	if connection != nil {
		_ = connection.Close()
		t.Fatal("unexpected accepted connection")
	}
	if err == nil || response == nil || response.StatusCode != expectedStatus {
		t.Fatalf("rejected connect: response=%v, err=%v, want status %d", response, err, expectedStatus)
	}
	_ = response.Body.Close()
	return nil
}

func read(t *testing.T, connection *websocket.Conn, expected []byte) {
	t.Helper()
	_ = connection.SetReadDeadline(time.Now().Add(2 * time.Second))
	kind, actual, err := connection.ReadMessage()
	if err != nil || kind != websocket.BinaryMessage || !bytes.Equal(actual, expected) {
		t.Fatalf("roundtrip kind=%d payload length=%d err=%v", kind, len(actual), err)
	}
}

func waitClosed(t *testing.T, connection *websocket.Conn) {
	t.Helper()
	_ = connection.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := connection.ReadMessage(); err == nil {
		t.Fatal("connection remained readable")
	} else if timeout, ok := err.(interface{ Timeout() bool }); ok && timeout.Timeout() {
		t.Fatal("server did not actively close connection")
	}
}

func TestAuthenticatedSingleUsePair(t *testing.T) {
	_, server := testServer(t, Config{MaxRooms: 1})
	createRoom(t, server.URL, "wrong-admin", http.StatusUnauthorized)
	i := createRoom(t, server.URL, testAdmin, http.StatusCreated)
	createRoom(t, server.URL, testAdmin, http.StatusTooManyRequests)
	dial(t, server.URL, i, "host", i.ClientToken, http.StatusUnauthorized)
	host := dial(t, server.URL, i, "host", i.HostToken, http.StatusSwitchingProtocols)
	dial(t, server.URL, i, "host", i.HostToken, http.StatusConflict)
	client := dial(t, server.URL, i, "client", i.ClientToken, http.StatusSwitchingProtocols)
	for _, pair := range [][2]*websocket.Conn{{host, client}, {client, host}} {
		payload := bytes.Repeat([]byte{0, 9, 255, 42}, 8192)
		if err := pair[0].WriteMessage(websocket.BinaryMessage, payload); err != nil {
			t.Fatal(err)
		}
		read(t, pair[1], payload)
	}
	_ = client.Close()
	waitClosed(t, host)
	dial(t, server.URL, i, "client", i.ClientToken, http.StatusNotFound)
	createRoom(t, server.URL, testAdmin, http.StatusCreated)
}

func TestRoomExpiryAndShutdownCloseWaitingSockets(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "expires", true: "shutdown"}[shutdown], func(t *testing.T) {
			service, server := testServer(t, Config{PairTimeout: 200 * time.Millisecond, MaxRooms: 1})
			i := createRoom(t, server.URL, testAdmin, http.StatusCreated)
			host := dial(t, server.URL, i, "host", i.HostToken, http.StatusSwitchingProtocols)
			if shutdown {
				_ = service.Close()
			}
			waitClosed(t, host)
			if shutdown {
				createRoom(t, server.URL, testAdmin, http.StatusServiceUnavailable)
			} else {
				createRoom(t, server.URL, testAdmin, http.StatusCreated)
			}
		})
	}
}

func TestDisconnectedWaitingHostReleasesRoom(t *testing.T) {
	service, server := testServer(t, Config{MaxRooms: 1, PairTimeout: time.Minute})
	invite := createRoom(t, server.URL, testAdmin, http.StatusCreated)
	host := dial(t, server.URL, invite, "host", invite.HostToken, http.StatusSwitchingProtocols)
	_ = host.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		service.mu.Lock()
		_, stale := service.rooms[invite.ID]
		service.mu.Unlock()
		if !stale {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("disconnected waiting host still occupies its room")
		}
		time.Sleep(10 * time.Millisecond)
	}
	createRoom(t, server.URL, testAdmin, http.StatusCreated)
}

func TestInvalidStreamClosesBothDirections(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind int
		size int
	}{{"text", websocket.TextMessage, 10}, {"oversize", websocket.BinaryMessage, MaxFrameSize + 1}} {
		t.Run(tc.name, func(t *testing.T) {
			_, server := testServer(t, Config{})
			i := createRoom(t, server.URL, testAdmin, http.StatusCreated)
			host := dial(t, server.URL, i, "host", i.HostToken, http.StatusSwitchingProtocols)
			client := dial(t, server.URL, i, "client", i.ClientToken, http.StatusSwitchingProtocols)
			_ = client.WriteMessage(tc.kind, make([]byte, tc.size))
			waitClosed(t, host)
			waitClosed(t, client)
		})
	}
}

func TestActiveIdleTimeoutAndServerClose(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "shutdown"}[shutdown], func(t *testing.T) {
			service, server := testServer(t, Config{IdleTimeout: 100 * time.Millisecond})
			i := createRoom(t, server.URL, testAdmin, http.StatusCreated)
			host := dial(t, server.URL, i, "host", i.HostToken, http.StatusSwitchingProtocols)
			client := dial(t, server.URL, i, "client", i.ClientToken, http.StatusSwitchingProtocols)
			if shutdown {
				_ = service.Close()
			}
			waitClosed(t, host)
			waitClosed(t, client)
		})
	}
}

func TestOneWayOutputKeepsRoomAlive(t *testing.T) {
	_, server := testServer(t, Config{IdleTimeout: 200 * time.Millisecond})
	i := createRoom(t, server.URL, testAdmin, http.StatusCreated)
	host := dial(t, server.URL, i, "host", i.HostToken, http.StatusSwitchingProtocols)
	client := dial(t, server.URL, i, "client", i.ClientToken, http.StatusSwitchingProtocols)
	ticker := time.NewTicker(30 * time.Millisecond)
	defer ticker.Stop()
	for n := 0; n < 15; n++ {
		<-ticker.C
		payload := []byte{byte(n)}
		if err := host.WriteMessage(websocket.BinaryMessage, payload); err != nil {
			t.Fatal(err)
		}
		read(t, client, payload)
	}
	// Once both directions become idle, both peers are actively closed.
	waitClosed(t, host)
	waitClosed(t, client)
}

func TestNoURLCredentialsOrBrowserOrigins(t *testing.T) {
	_, server := testServer(t, Config{})
	for _, tc := range []struct{ path, origin string }{{"/v1/rooms?token=not-secret", ""}, {"/v1/rooms", "https://untrusted.example"}} {
		r, _ := http.NewRequest(http.MethodPost, server.URL+tc.path, nil)
		r.Header.Set("Authorization", "Bearer "+testAdmin)
		r.Header.Set("Origin", tc.origin)
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusBadRequest || strings.Contains(string(body), testAdmin) {
			t.Fatalf("unexpected rejection: %d %q", response.StatusCode, body)
		}
	}
}

func TestSavedDeviceReceivesNewInvitationAfterDisconnect(t *testing.T) {
	_, server := testServer(t, Config{})
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	secret := base64.RawURLEncoding.EncodeToString(key)
	digest := sha256.Sum256(key)
	body := fmt.Sprintf(`{"lookup_hash":"%x"}`, digest)
	create := func(status int) Invitation {
		t.Helper()
		req, _ := http.NewRequest("POST", server.URL+"/v1/rooms", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+testAdmin)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != status {
			t.Fatalf("room: %d, want %d", res.StatusCode, status)
		}
		var i Invitation
		if status == 201 {
			if err = json.NewDecoder(res.Body).Decode(&i); err != nil {
				t.Fatal(err)
			}
		}
		return i
	}
	lookup := func(token string) (int, Invitation) {
		t.Helper()
		req, _ := http.NewRequest("GET", server.URL+"/v1/invitation", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var i Invitation
		if res.StatusCode == 200 {
			if err = json.NewDecoder(res.Body).Decode(&i); err != nil {
				t.Fatal(err)
			}
			if i.HostToken != "" || res.Header.Get("Cache-Control") != "no-store" {
				t.Fatal("lookup exposed a host credential or a cacheable ticket")
			}
		}
		return res.StatusCode, i
	}
	first := create(201)
	if status, _ := lookup(secret); status != 404 {
		t.Fatal("advertised a room before host joined")
	}
	host := dial(t, server.URL, first, "host", first.HostToken, 101)
	create(409)
	if status, _ := lookup(base64.RawURLEncoding.EncodeToString(make([]byte, 32))); status != 404 {
		t.Fatal("unknown device looked up a room")
	}
	wait := func(i Invitation) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			status, found := lookup(secret)
			if status == 200 {
				if found.ID != i.ID || found.ClientToken != i.ClientToken {
					t.Fatal("looked up another device or stale ticket")
				}
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("room did not become discoverable")
	}
	wait(first)
	client := dial(t, server.URL, first, "client", first.ClientToken, 101)
	if status, _ := lookup(secret); status != 404 {
		t.Fatal("claimed client ticket could be fetched")
	}
	client.Close()
	waitClosed(t, host)
	if status, _ := lookup(secret); status != 404 {
		t.Fatal("closed room still discoverable")
	}
	second := create(201)
	dial(t, server.URL, second, "host", second.HostToken, 101)
	if first.ID == second.ID || first.ClientToken == second.ClientToken {
		t.Fatal("reused a one-time transport credential")
	}
	wait(second)
}

func TestPublicRoomCreationKeepsRoleAuthentication(t *testing.T) {
	for _, public := range []bool{false, true} {
		_, server := testServer(t, Config{PublicRooms: public})
		response, err := http.Post(server.URL+"/v1/rooms", "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if !public {
			if response.StatusCode != http.StatusUnauthorized {
				t.Fatal("private relay allowed anonymous room creation")
			}
			continue
		}
		var room Invitation
		if response.StatusCode != http.StatusCreated || json.NewDecoder(response.Body).Decode(&room) != nil {
			t.Fatal("public relay did not create a room")
		}
		dial(t, server.URL, room, "client", room.HostToken, http.StatusUnauthorized)
		dial(t, server.URL, room, "host", room.HostToken, http.StatusSwitchingProtocols)
	}
}
