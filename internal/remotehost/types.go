// Package remotehost owns saved remote hosts and authenticated connections.
// It does not execute workloads, stop remote services, or expose SSH credentials
// to the workbench page.
package remotehost

import (
	"context"
	"net/http"
)

const APIProtocol = 1

type AuthConfig struct {
	Kind         string `json:"kind"`
	IdentityFile string `json:"identity_file,omitempty"`
}

type HostKey struct {
	Algorithm   string `json:"algorithm"`
	Fingerprint string `json:"fingerprint"`
}

type HostProfile struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Host       string     `json:"host"`
	Port       int        `json:"port"`
	User       string     `json:"user"`
	Auth       AuthConfig `json:"auth"`
	HostKey    *HostKey   `json:"host_key,omitempty"`
	Executable string     `json:"executable,omitempty"`
	ConfigPath string     `json:"config_path,omitempty"`
}

type PublicManifest struct {
	APIProtocol  int             `json:"api_protocol"`
	InstanceID   string          `json:"instance_id"`
	Version      string          `json:"version"`
	Platform     string          `json:"platform"`
	Runtime      string          `json:"runtime"`
	Capabilities map[string]bool `json:"capabilities"`
}

// ServerManifest is the private SSH command response. Never return it from a
// browser API: Ticket is consumed only inside the connector.
type ServerManifest struct {
	APIProtocol  int             `json:"api_protocol"`
	InstanceID   string          `json:"instance_id"`
	Version      string          `json:"version"`
	Platform     string          `json:"platform"`
	Runtime      string          `json:"runtime"`
	Capabilities map[string]bool `json:"capabilities"`
	Origin       string          `json:"origin"`
	Ticket       string          `json:"ticket"`
}

func (m ServerManifest) Public() PublicManifest {
	return PublicManifest{m.APIProtocol, m.InstanceID, m.Version, m.Platform, m.Runtime, m.Capabilities}
}

// ConnectSecrets are never serialized or persisted. Connect copies the
// passphrase before returning and clears that copy after SSH authentication.
type ConnectSecrets struct {
	Deploy     bool   `json:"-"`
	Passphrase []byte `json:"-"`
}

type ConnectionRef struct {
	ID         string `json:"connection_id"`
	HostID     string `json:"host_id"`
	InstanceID string `json:"instance_id"`
}

type ConnectionState struct {
	State        string          `json:"state"`
	ConnectionID string          `json:"connection_id,omitempty"`
	HostID       string          `json:"host_id,omitempty"`
	InstanceID   string          `json:"instance_id,omitempty"`
	Phase        string          `json:"phase,omitempty"`
	Message      string          `json:"message,omitempty"`
	ErrorCode    string          `json:"error_code,omitempty"`
	HostKey      *HostKey        `json:"host_key,omitempty"`
	Manifest     *PublicManifest `json:"manifest,omitempty"`
}

type Error struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	HostKey *HostKey `json:"host_key,omitempty"`
}

func (e *Error) Error() string { return e.Message }

func failure(code, message string) error { return &Error{Code: code, Message: message} }

// Session remains pinned to one authenticated remote instance. Transport is
// suitable for HTTP reverse proxies, including WebSocket upgrades and SSE.
// Its URL must use Origin; it injects the private upstream session cookie.
type Session struct {
	Ref       ConnectionRef
	Manifest  PublicManifest
	Client    *http.Client
	Transport http.RoundTripper
	Context   context.Context
	Origin    string
	close     func() error
}

func (s *Session) Close() error {
	if s == nil || s.close == nil {
		return nil
	}
	return s.close()
}
