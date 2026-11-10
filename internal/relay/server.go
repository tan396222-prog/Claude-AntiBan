// Package relay pairs two outbound WebSocket connections. It forwards opaque
// bytes only; peer identity and application authorization belong to the hosts.
package relay

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const MaxFrameSize = 64 << 10

type Config struct {
	PublicRooms bool
	AdminToken  string
	MaxRooms    int
	PairTimeout time.Duration
	IdleTimeout time.Duration
}

// Invitation contains short-lived transport capabilities, not device identity.
// A client must authenticate its peer inside the tunnel before sending data.
type Invitation struct {
	ID          string    `json:"id"`
	HostToken   string    `json:"host_token"`
	ClientToken string    `json:"client_token"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type endpoint struct {
	token   [32]byte
	claimed bool
	socket  *websocket.Conn
}

type room struct {
	id               string
	expiresAt        time.Time
	host             endpoint
	client           endpoint
	ready            chan struct{}
	done             chan struct{}
	timer            *time.Timer
	paired           bool
	lastActivity     time.Time
	lookupHash       [32]byte
	discoverable     bool
	clientInvitation string
}

type Server struct {
	config Config
	admin  [32]byte
	mu     sync.Mutex
	rooms  map[string]*room
	slots  map[[32]byte]*room
	closed bool
}

func New(config Config) (*Server, error) {
	if len(config.AdminToken) < 32 || strings.ContainsAny(config.AdminToken, " \t\r\n") {
		return nil, errors.New("relay admin token must contain at least 32 bytes without whitespace")
	}
	if config.MaxRooms == 0 {
		config.MaxRooms = 128
	}
	if config.PairTimeout == 0 {
		config.PairTimeout = 2 * time.Minute
	}
	if config.IdleTimeout == 0 {
		config.IdleTimeout = 5 * time.Minute
	}
	if config.MaxRooms < 1 || config.PairTimeout < 0 || config.IdleTimeout < 0 {
		return nil, errors.New("relay limits must be positive")
	}
	admin := sha256.Sum256([]byte(config.AdminToken))
	config.AdminToken = ""
	return &Server{config: config, admin: admin, rooms: make(map[string]*room), slots: make(map[[32]byte]*room)}, nil
}

func secret() string {
	var value [32]byte
	_, _ = rand.Read(value[:]) // crypto/rand.Read always fills the buffer or terminates.
	return base64.RawURLEncoding.EncodeToString(value[:])
}

func authorized(r *http.Request, expected [32]byte) bool {
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return false
	}
	value := strings.TrimPrefix(values[0], "Bearer ")
	if value == "" || strings.ContainsAny(value, " \t\r\n") {
		return false
	}
	actual := sha256.Sum256([]byte(value))
	return subtle.ConstantTimeCompare(actual[:], expected[:]) == 1
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Transport credentials only travel in headers. No browser-origin clients
	// are accepted: the mobile native transport owns the TLS tunnel.
	if r.URL.RawQuery != "" || r.Header.Get("Origin") != "" {
		http.Error(w, "unsupported request", http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			http.Error(w, "stopping", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok\n"))
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/v1/rooms" {
		s.create(w, r)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/v1/invitation" {
		s.lookup(w, r)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if r.Method == http.MethodGet && len(parts) == 4 && parts[0] == "v1" && parts[1] == "rooms" && (parts[3] == "host" || parts[3] == "client") {
		s.join(w, r, parts[2], parts[3])
		return
	}
	http.NotFound(w, r)
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	if !(s.config.PublicRooms && len(r.Header.Values("Authorization")) == 0) && !authorized(r, s.admin) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var hash [32]byte
	discoverable := r.ContentLength != 0
	if discoverable {
		var body struct {
			Hash string `json:"lookup_hash"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF {
			http.Error(w, "invalid room registration", http.StatusBadRequest)
			return
		}
		digest, err := hex.DecodeString(body.Hash)
		if err != nil || len(digest) != len(hash) || hex.EncodeToString(digest) != body.Hash {
			http.Error(w, "invalid room registration", http.StatusBadRequest)
			return
		}
		copy(hash[:], digest)
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		http.Error(w, "stopping", http.StatusServiceUnavailable)
		return
	}
	if len(s.rooms) >= s.config.MaxRooms {
		s.mu.Unlock()
		http.Error(w, "relay capacity reached", http.StatusTooManyRequests)
		return
	}
	if discoverable && s.slots[hash] != nil {
		s.mu.Unlock()
		http.Error(w, "existing device connection", http.StatusConflict)
		return
	}
	i := Invitation{ID: secret(), HostToken: secret(), ClientToken: secret(), ExpiresAt: time.Now().Add(s.config.PairTimeout)}
	room := &room{id: i.ID, expiresAt: i.ExpiresAt, host: endpoint{token: sha256.Sum256([]byte(i.HostToken))},
		client: endpoint{token: sha256.Sum256([]byte(i.ClientToken))}, ready: make(chan struct{}), done: make(chan struct{})}
	s.rooms[i.ID] = room
	if discoverable {
		room.lookupHash, room.discoverable, room.clientInvitation = hash, true, i.ClientToken
		s.slots[hash] = room
	}
	room.timer = time.AfterFunc(s.config.PairTimeout, func() { s.expire(room) })
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(i)
}

// A saved device resolves a fresh transport ticket. Both device certificates
// are still independently verified end-to-end when it uses that ticket.
func (s *Server) lookup(w http.ResponseWriter, r *http.Request) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		http.NotFound(w, r)
		return
	}
	value, ok := strings.CutPrefix(values[0], "Bearer ")
	key, err := base64.RawURLEncoding.DecodeString(value)
	if !ok || err != nil || len(key) != 32 || base64.RawURLEncoding.EncodeToString(key) != value {
		http.NotFound(w, r)
		return
	}
	hash := sha256.Sum256(key)
	clear(key)
	s.mu.Lock()
	room := s.slots[hash]
	if room == nil || room.host.socket == nil || room.client.claimed || !time.Now().Before(room.expiresAt) {
		s.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	response := struct {
		ID        string    `json:"id"`
		Token     string    `json:"client_token"`
		ExpiresAt time.Time `json:"expires_at"`
	}{room.id, room.clientInvitation, room.expiresAt}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func (s *Server) join(w http.ResponseWriter, r *http.Request, id, role string) {
	s.mu.Lock()
	room := s.rooms[id]
	if room == nil || s.closed {
		s.mu.Unlock()
		http.Error(w, "connection unavailable", http.StatusNotFound)
		return
	}
	endpoint := &room.client
	if role == "host" {
		endpoint = &room.host
	}
	if !authorized(r, endpoint.token) {
		s.mu.Unlock()
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if endpoint.claimed || room.paired || !time.Now().Before(room.expiresAt) {
		s.mu.Unlock()
		http.Error(w, "connection invitation consumed or expired", http.StatusConflict)
		return
	}
	endpoint.claimed = true
	s.mu.Unlock()
	defer s.remove(room)

	upgrader := websocket.Upgrader{HandshakeTimeout: 5 * time.Second, CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" }}
	connection, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer connection.Close()
	connection.SetReadLimit(MaxFrameSize)
	s.mu.Lock()
	if s.rooms[id] != room || s.closed {
		s.mu.Unlock()
		return
	}
	endpoint.socket = connection
	if room.host.socket != nil && room.client.socket != nil {
		room.paired = true
		room.clientInvitation = ""
		room.timer.Stop()
		room.lastActivity = time.Now()
		room.timer = time.AfterFunc(s.config.IdleTimeout, func() { s.expireIdle(room) })
		close(room.ready)
	}
	s.mu.Unlock()

	// Read even before the peer joins. A waiting host can disconnect on
	// restart, and its saved pairing must immediately be free to register again.
	var other *websocket.Conn
	for {
		kind, data, err := connection.ReadMessage()
		if err != nil || kind != websocket.BinaryMessage {
			return
		}
		if other == nil {
			select {
			case <-room.ready:
			case <-room.done:
				return
			}
			s.mu.Lock()
			other = room.host.socket
			if role == "host" {
				other = room.client.socket
			}
			s.mu.Unlock()
		}
		s.mu.Lock()
		live := s.rooms[room.id] == room
		if live {
			room.lastActivity = time.Now()
		}
		s.mu.Unlock()
		if !live {
			return
		}
		_ = other.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := other.WriteMessage(websocket.BinaryMessage, data); err != nil {
			return
		}
	}
}

// A receive-only client is active while the host streams output. Account for
// traffic in either direction instead of timing out the quieter endpoint.
func (s *Server) expireIdle(room *room) {
	s.mu.Lock()
	if s.rooms[room.id] != room {
		s.mu.Unlock()
		return
	}
	remaining := time.Until(room.lastActivity.Add(s.config.IdleTimeout))
	if remaining > 0 {
		room.timer.Reset(remaining)
		s.mu.Unlock()
		return
	}
	connections := s.removeLocked(room)
	s.mu.Unlock()
	closeConnections(connections)
}

// expire only removes waiting rooms; active connections have an idle timeout.
func (s *Server) expire(room *room) {
	s.mu.Lock()
	if s.rooms[room.id] != room || room.paired {
		s.mu.Unlock()
		return
	}
	connections := s.removeLocked(room)
	s.mu.Unlock()
	closeConnections(connections)
}

func (s *Server) removeLocked(room *room) []*websocket.Conn {
	if s.rooms[room.id] != room {
		return nil
	}
	delete(s.rooms, room.id)
	if room.discoverable {
		delete(s.slots, room.lookupHash)
		room.clientInvitation = ""
	}
	room.timer.Stop()
	close(room.done)
	return []*websocket.Conn{room.host.socket, room.client.socket}
}

func closeConnections(connections []*websocket.Conn) {
	for _, connection := range connections {
		if connection != nil {
			_ = connection.Close()
		}
	}
}

func (s *Server) remove(room *room) {
	s.mu.Lock()
	connections := s.removeLocked(room)
	s.mu.Unlock()
	closeConnections(connections)
}

// Close rejects new rooms and closes all upgraded connections. HTTP shutdown
// alone does not close hijacked WebSockets; callers must also call Close.
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	var connections []*websocket.Conn
	for _, room := range s.rooms {
		connections = append(connections, s.removeLocked(room)...)
	}
	s.mu.Unlock()
	closeConnections(connections)
	return nil
}
