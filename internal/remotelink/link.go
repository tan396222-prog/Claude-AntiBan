// Package remotelink authenticates an already paired peer and carries TLS over
// binary WebSocket messages. The relay is not trusted with plaintext or peer
// identity. Pairing, persistent identity storage, and application integration
// (including Claude Desktop) are outside this package.
package remotelink

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Protocol is the required TLS application protocol.
	Protocol = "cage-remote/1"
	// MaxMessageSize limits each incoming WebSocket binary message, including
	// all its fragments. Outbound messages are at most 32 KiB.
	MaxMessageSize   = 64 << 10
	handshakeTimeout = 15 * time.Second
)

// Identity contains a TLS certificate and its public-key fingerprint. Pin uses
// "sha256:" followed by standard, padded Base64 of SHA-256(SPKI DER).
// Keep Certificate.PrivateKey private; exchange pins through the pairing flow.
type Identity struct {
	Certificate tls.Certificate
	Pin         string
}

// NewIdentity creates a temporary Ed25519 identity valid for 24 hours. It is
// intended for transport validation, not production identity persistence.
func NewIdentity() (*Identity, error) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate identity key: %w", err)
	}
	return IdentityFromKey(private)
}

// IdentityFromKey issues a short-lived certificate for an existing device key.
// The peer pin remains stable when this certificate is renewed.
func IdentityFromKey(key ed25519.PrivateKey) (*Identity, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid Ed25519 identity key")
	}
	return IdentityFromSigner(key)
}

// IdentityFromSigner also supports non-exportable P-256 device keys. TLS and
// certificate issuance call Sign on the supplied signer; the private key need
// not be readable by the application.
func IdentityFromSigner(signer crypto.Signer) (*Identity, error) {
	if signer == nil {
		return nil, errors.New("device identity signer is required")
	}
	public := signer.Public()
	switch key := public.(type) {
	case ed25519.PublicKey:
		if len(key) != ed25519.PublicKeySize {
			return nil, errors.New("invalid Ed25519 identity public key")
		}
	case *ecdsa.PublicKey:
		if key == nil || key.Curve != elliptic.P256() {
			return nil, errors.New("device ECDSA signer must use P-256")
		}
	default:
		return nil, errors.New("device identity signer must use Ed25519 or P-256")
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate certificate serial: %w", err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Cage temporary remote identity"},
		NotBefore:    now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, signer)
	if err != nil {
		return nil, fmt.Errorf("create identity certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parse identity certificate: %w", err)
	}
	digest := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	return &Identity{
		Certificate: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: signer, Leaf: leaf},
		Pin:         "sha256:" + base64.StdEncoding.EncodeToString(digest[:]),
	}, nil
}

// Client performs a mutually authenticated TLS 1.3 client handshake. expectedPin
// must be the server identity established by pairing, never supplied by the
// relay. Client takes exclusive ownership of ws and closes it on any failure.
// ctx applies to the handshake only; a successful connection outlives ctx.
func Client(ctx context.Context, ws *websocket.Conn, identity *Identity, expectedPin string) (*tls.Conn, error) {
	return handshake(ctx, ws, identity, expectedPin, false, false)
}

// Server is the server counterpart of Client. expectedPin must identify the
// already paired client. It has the same ownership and context rules as Client.
func Server(ctx context.Context, ws *websocket.Conn, identity *Identity, expectedPin string) (*tls.Conn, error) {
	return handshake(ctx, ws, identity, expectedPin, true, false)
}

// ServerAfterJoin allows up to two minutes for the other device to enter a
// relay room. The normal handshake deadline starts when its first binary
// message arrives. The caller owns the room invitation and its expiry.
func ServerAfterJoin(ctx context.Context, ws *websocket.Conn, identity *Identity, expectedPin string) (*tls.Conn, error) {
	return handshake(ctx, ws, identity, expectedPin, true, true)
}

func peerVerifier(pin string) (func(tls.ConnectionState) error, error) {
	encoded, ok := strings.CutPrefix(pin, "sha256:")
	digest, err := base64.StdEncoding.DecodeString(encoded)
	if !ok || err != nil || len(digest) != sha256.Size || base64.StdEncoding.EncodeToString(digest) != encoded {
		return nil, errors.New("peer pin must be sha256: followed by padded Base64 of a SHA-256 SPKI digest")
	}
	return func(state tls.ConnectionState) error {
		if state.Version != tls.VersionTLS13 || state.NegotiatedProtocol != Protocol {
			return errors.New("peer did not negotiate the required TLS version and application protocol")
		}
		if len(state.PeerCertificates) == 0 {
			return errors.New("peer did not provide an identity certificate")
		}
		certificate := state.PeerCertificates[0]
		now := time.Now()
		if now.Before(certificate.NotBefore) || !now.Before(certificate.NotAfter) {
			return errors.New("peer identity certificate is outside its validity period")
		}
		actual := sha256.Sum256(certificate.RawSubjectPublicKeyInfo)
		if subtle.ConstantTimeCompare(actual[:], digest) != 1 {
			return errors.New("peer identity does not match the paired public key")
		}
		return nil
	}, nil
}

func handshake(ctx context.Context, ws *websocket.Conn, identity *Identity, expectedPin string, server, waitForJoin bool) (_ *tls.Conn, err error) {
	if ws == nil {
		return nil, errors.New("WebSocket connection is required")
	}
	defer func() {
		if err != nil {
			_ = ws.Close()
		}
	}()
	verify, err := peerVerifier(expectedPin)
	if err != nil {
		return nil, err
	}
	if identity == nil || len(identity.Certificate.Certificate) == 0 || identity.Certificate.PrivateKey == nil {
		return nil, errors.New("local identity certificate and private key are required")
	}
	config := &tls.Config{
		MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13,
		NextProtos:             []string{Protocol},
		Certificates:           []tls.Certificate{identity.Certificate},
		VerifyConnection:       verify,
		SessionTicketsDisabled: true,
	}
	stream := newWebSocketConn(ws)
	if waitForJoin {
		wait, cancel := context.WithTimeout(ctx, 2*time.Minute)
		stop := context.AfterFunc(wait, func() { _ = stream.Close() })
		kind, first, readErr := ws.NextReader()
		stop()
		waitErr := wait.Err()
		cancel()
		if waitErr != nil {
			return nil, fmt.Errorf("waiting for paired device: %w", waitErr)
		}
		if readErr != nil || kind != websocket.BinaryMessage {
			return nil, errors.New("peer did not send a binary TLS handshake")
		}
		// NextReader has consumed the message header only. The first TLS record
		// remains in the bounded WebSocket reader used by webSocketConn.Read.
		stream.reader = first
	}
	var connection *tls.Conn
	if server {
		config.ClientAuth = tls.RequireAnyClientCert
		connection = tls.Server(stream, config)
	} else {
		// Paired public keys replace Web PKI trust. VerifyConnection is mandatory
		// and checks the exact key, certificate validity, TLS version, and ALPN.
		config.InsecureSkipVerify = true
		connection = tls.Client(stream, config)
	}
	ctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	if err := stream.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("set handshake deadline: %w", err)
	}
	stop := context.AfterFunc(ctx, func() { _ = stream.Close() })
	err = connection.HandshakeContext(ctx)
	stop()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("remote TLS handshake: %w", ctx.Err())
	}
	if err != nil {
		return nil, fmt.Errorf("remote TLS handshake: %w", err)
	}
	if err := stream.SetDeadline(time.Time{}); err != nil {
		return nil, fmt.Errorf("clear handshake deadline: %w", err)
	}
	return connection, nil
}
