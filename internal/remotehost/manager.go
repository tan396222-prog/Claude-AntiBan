package remotehost

import (
	"context"
	"errors"
	"maps"
	"sync"
)

type Manager struct {
	mu      sync.Mutex
	store   *Store
	state   ConnectionState
	session *Session
	cancel  context.CancelFunc
	closed  bool
	workers sync.WaitGroup
}

func NewManager(store *Store) *Manager {
	return &Manager{store: store, state: ConnectionState{State: "disconnected"}}
}

func cloneState(state ConnectionState) ConnectionState {
	if state.HostKey != nil {
		key := *state.HostKey
		state.HostKey = &key
	}
	if state.Manifest != nil {
		manifest := *state.Manifest
		manifest.Capabilities = maps.Clone(manifest.Capabilities)
		state.Manifest = &manifest
	}
	return state
}

func (m *Manager) Snapshot() ConnectionState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneState(m.state)
}

// Connect starts asynchronously. The caller can poll Snapshot. A previous
// connection must be disconnected first; failure never switches host silently.
func (m *Manager) Connect(hostID string, secrets ConnectSecrets) (ConnectionState, error) {
	p, err := m.store.Get(hostID)
	if err != nil {
		return ConnectionState{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ConnectionState{}, failure("manager_closed", "远程连接服务已关闭")
	}
	if m.state.State == "connecting" || m.state.State == "connected" {
		return cloneState(m.state), failure("connection_active", "请先断开当前连接，再连接其他主机")
	}
	if m.cancel != nil {
		m.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	id := newID()
	m.state = ConnectionState{State: "connecting", ConnectionID: id, HostID: p.ID, Phase: "ssh_connect", Message: "正在连接远程主机"}
	private := ConnectSecrets{Deploy: secrets.Deploy, Passphrase: append([]byte(nil), secrets.Passphrase...)}
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		m.run(ctx, p, id, private)
	}()
	return cloneState(m.state), nil
}

func (m *Manager) run(ctx context.Context, p HostProfile, id string, secrets ConnectSecrets) {
	defer clear(secrets.Passphrase)
	session, err := connect(ctx, p, secrets, func(phase string) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.state.ConnectionID == id && m.state.State == "connecting" {
			m.state.Phase = phase
		}
	})
	m.mu.Lock()
	if m.state.ConnectionID != id || m.state.State != "connecting" || ctx.Err() != nil {
		m.mu.Unlock()
		if session != nil {
			session.Close()
		}
		return
	}
	if err != nil {
		m.state.State = "failed"
		m.state.Message = "远程连接未完成"
		m.state.ErrorCode = "connection_failed"
		var structured *Error
		if errors.As(err, &structured) {
			m.state.ErrorCode = structured.Code
			m.state.Message = structured.Message
			m.state.HostKey = structured.HostKey
		}
		if m.cancel != nil {
			m.cancel()
			m.cancel = nil
		}
		m.mu.Unlock()
		return
	}
	session.Ref = ConnectionRef{ID: id, HostID: p.ID, InstanceID: session.Manifest.InstanceID}
	m.session = session
	manifest := session.Manifest
	m.state = ConnectionState{State: "connected", ConnectionID: id, HostID: p.ID, InstanceID: manifest.InstanceID, Message: "已连接", Manifest: &manifest}
	m.mu.Unlock()
	<-session.Context.Done()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.ConnectionID == id && m.state.State == "connected" {
		m.session = nil
		m.state.State = "failed"
		m.state.ErrorCode = "connection_lost"
		m.state.Message = "远程连接已中断，请重新连接并核对云端任务"
	}
}

// Acquire refuses obsolete connection generations. Every runtime request and
// native file operation must carry the ID captured by its originating view.
func (m *Manager) Acquire(connectionID string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if connectionID == "" || m.state.State != "connected" || m.state.ConnectionID != connectionID || m.session == nil || m.session.Context.Err() != nil {
		return nil, failure("connection_changed", "连接已断开或切换，请重新连接后重试")
	}
	return m.session, nil
}

func (m *Manager) Disconnect(connectionID string) error {
	m.mu.Lock()
	if m.state.State == "disconnected" {
		m.mu.Unlock()
		return nil
	}
	if connectionID == "" || m.state.ConnectionID != connectionID {
		m.mu.Unlock()
		return failure("connection_changed", "连接已切换，未断开其他连接")
	}
	cancel, session := m.cancel, m.session
	m.cancel = nil
	m.session = nil
	m.state = ConnectionState{State: "disconnected"}
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if session != nil {
		return session.Close()
	}
	return nil
}

func (m *Manager) Close() error {
	m.mu.Lock()
	m.closed = true
	cancel, session := m.cancel, m.session
	m.cancel = nil
	m.session = nil
	m.state = ConnectionState{State: "disconnected"}
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	var err error
	if session != nil {
		err = session.Close()
	}
	m.workers.Wait()
	return err
}
