// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ssh

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

const (
	testUser     = "tester"
	testPassword = "secret"
)

// trustedKeys holds the keys trusted for each node, as a project's host key
// store would.
type trustedKeys map[string]gossh.PublicKey

func (k trustedKeys) Get(_ context.Context, node string) (inventory.TrustedHostKey, error) {
	key, ok := k[node]
	if !ok {
		return inventory.TrustedHostKey{}, inventory.ErrNoTrustedHostKey
	}
	return inventory.NewTrustedHostKey(node, key, "tester", time.Now())
}

func startTestServer(t *testing.T) *harness.SSHServer {
	t.Helper()
	return harness.StartSSHServer(t, harness.SSHServerOptions{User: testUser, Password: testPassword})
}

func newPublicKey(t *testing.T) gossh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	key, err := gossh.NewPublicKey(pub)
	require.NoError(t, err)
	return key
}

func authorizedKey(key gossh.PublicKey) string {
	return strings.TrimSpace(string(gossh.MarshalAuthorizedKey(key)))
}

// connect opens a session over cfg and closes it.
func connect(t *testing.T, cfg *Config) error {
	t.Helper()
	cfg.User = testUser
	cfg.SSHAuth = ir.SSHAuth{Password: testPassword}
	cfg.Timeout = 10 * time.Second
	if cfg.Bastion != nil {
		cfg.Bastion.User = testUser
		cfg.Bastion.SSHAuth = ir.SSHAuth{Password: testPassword}
	}
	ctx := context.Background()
	client, err := NewClient(ctx, cfg)
	require.NoError(t, err)
	conn, session, err := client.NewSession(ctx)
	if err != nil {
		return err
	}
	_ = session.Close()
	return conn.Close()
}

func requireHostKeyError(t *testing.T, err error) *hostKeyError {
	t.Helper()
	hostKeyErr, ok := errors.AsType[*hostKeyError](err)
	require.True(t, ok, "want a host key error, got %v", err)
	return hostKeyErr
}

// A node connection refuses a key that is neither the node's pinned key nor
// its trusted key, unless it accepts any key, and the server sees no
// credential.
func TestNodeHostKeyRefusedBeforeCredentials(t *testing.T) {
	t.Parallel()

	other := newPublicKey(t)
	tests := []struct {
		name     string
		hostKey  func(server *harness.SSHServer) *NodeHostKey
		expected []string
	}{
		{
			name: "Unknown",
			hostKey: func(*harness.SSHServer) *NodeHostKey {
				return &NodeHostKey{Node: "web01", Trusted: trustedKeys{}}
			},
		},
		{
			name: "TrustedKeyChanged",
			hostKey: func(*harness.SSHServer) *NodeHostKey {
				return &NodeHostKey{Node: "web01", Trusted: trustedKeys{"web01": other}}
			},
			expected: []string{gossh.FingerprintSHA256(other)},
		},
		{
			name: "PinnedKeyChanged",
			hostKey: func(*harness.SSHServer) *NodeHostKey {
				return &NodeHostKey{Node: "web01", Pinned: authorizedKey(other)}
			},
			expected: []string{gossh.FingerprintSHA256(other)},
		},
		{
			name: "KeyTrustedForAnotherNode",
			hostKey: func(server *harness.SSHServer) *NodeHostKey {
				return &NodeHostKey{Node: "web01", Trusted: trustedKeys{"web02": server.HostKey}}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := startTestServer(t)

			err := connect(t, &Config{Host: "127.0.0.1", Port: server.Port, NodeHostKey: tt.hostKey(server)})

			hostKeyErr := requireHostKeyError(t, err)
			assert.Equal(t, hopNode, hostKeyErr.hop)
			assert.Equal(t, "web01", hostKeyErr.node)
			assert.Equal(t, gossh.FingerprintSHA256(server.HostKey), hostKeyErr.presented)
			assert.Equal(t, tt.expected, hostKeyErr.expected)
			assert.Zero(t, server.AuthAttempts(), "no credential may reach the host")
		})
	}
}

func TestNodeHostKeyAccepted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hostKey func(server *harness.SSHServer) *NodeHostKey
	}{
		{
			name: "Pinned",
			hostKey: func(server *harness.SSHServer) *NodeHostKey {
				return &NodeHostKey{Node: "web01", Pinned: authorizedKey(server.HostKey)}
			},
		},
		{
			name: "Trusted",
			hostKey: func(server *harness.SSHServer) *NodeHostKey {
				return &NodeHostKey{Node: "web01", Trusted: trustedKeys{"web01": server.HostKey}}
			},
		},
		{
			// The node's host_key is outdated, and the key trusted since is
			// accepted.
			name: "TrustedBesidePinned",
			hostKey: func(server *harness.SSHServer) *NodeHostKey {
				return &NodeHostKey{
					Node: "web01", Pinned: authorizedKey(newPublicKey(t)),
					Trusted: trustedKeys{"web01": server.HostKey},
				}
			},
		},
		{
			name: "AcceptAnyKey",
			hostKey: func(*harness.SSHServer) *NodeHostKey {
				return &NodeHostKey{Node: "web01", AcceptAnyKey: true, Trusted: trustedKeys{}}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := startTestServer(t)

			err := connect(t, &Config{Host: "127.0.0.1", Port: server.Port, NodeHostKey: tt.hostKey(server)})

			require.NoError(t, err)
			assert.Equal(t, 1, server.AuthAttempts())
		})
	}
}

// Each hop of a connection through a bastion is checked with the keys of its
// own node, and a refused hop receives no credential.
func TestNodeHostKeyThroughBastion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// trusted returns the trusted keys, given the servers' keys.
		trusted         func(bastion, target gossh.PublicKey) trustedKeys
		refusedHop      string
		bastionAttempts int
		targetAttempts  int
	}{
		{
			name:            "BothTrusted",
			trusted:         func(b, tg gossh.PublicKey) trustedKeys { return trustedKeys{"jump": b, "web01": tg} },
			bastionAttempts: 1,
			targetAttempts:  1,
		},
		{
			name:       "BastionUnknown",
			trusted:    func(_, tg gossh.PublicKey) trustedKeys { return trustedKeys{"web01": tg} },
			refusedHop: hopBastion,
		},
		{
			name:            "TargetUnknown",
			trusted:         func(b, _ gossh.PublicKey) trustedKeys { return trustedKeys{"jump": b} },
			refusedHop:      hopNode,
			bastionAttempts: 1,
		},
		{
			// The target's key trusted under the bastion's name does not let
			// the target in.
			name:            "KeysSwapped",
			trusted:         func(b, tg gossh.PublicKey) trustedKeys { return trustedKeys{"jump": tg, "web01": b} },
			refusedHop:      hopBastion,
			bastionAttempts: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			bastion := startTestServer(t)
			target := startTestServer(t)
			trusted := tt.trusted(bastion.HostKey, target.HostKey)

			err := connect(t, &Config{
				Host:        "127.0.0.1",
				Port:        target.Port,
				NodeHostKey: &NodeHostKey{Node: "web01", Trusted: trusted},
				Bastion: &BastionConfig{
					Host:        "127.0.0.1",
					Port:        bastion.Port,
					NodeHostKey: &NodeHostKey{Node: "jump", Trusted: trusted},
				},
			})

			if tt.refusedHop == "" {
				require.NoError(t, err)
			} else {
				hostKeyErr := requireHostKeyError(t, err)
				assert.Equal(t, tt.refusedHop, hostKeyErr.hop)
			}
			assert.Equal(t, tt.bastionAttempts, bastion.AuthAttempts(), "credentials sent to the bastion")
			assert.Equal(t, tt.targetAttempts, target.AuthAttempts(), "credentials sent to the target")
		})
	}
}

func TestNodeHostKeyBastionNamesNoNode(t *testing.T) {
	t.Parallel()

	_, err := NewClient(context.Background(), &Config{
		Host:        "127.0.0.1",
		SSHAuth:     ir.SSHAuth{Password: testPassword},
		NodeHostKey: &NodeHostKey{Node: "web01"},
		Bastion:     &BastionConfig{Host: "127.0.0.1", SSHAuth: ir.SSHAuth{Password: testPassword}},
	})

	assert.ErrorContains(t, err, `the bastion of node "web01" names no node`)
}

// A server presents one host key per handshake, of the first offered type it
// holds. A node whose expected keys differ in type is tried once per type, so
// the expected key the server still holds is accepted, whether or not the
// server holds a key of the other type, and a refused handshake sends no
// credential.
func TestNodeHostKeyServerWithSeveralKeys(t *testing.T) {
	t.Parallel()

	ed25519Host := newEd25519Signer(t)
	ecdsaHost := newECDSASigner(t)
	outdatedECDSA := newECDSASigner(t).PublicKey()
	outdatedPin := &NodeHostKey{
		Node: "web01", Pinned: authorizedKey(outdatedECDSA),
		Trusted: trustedKeys{"web01": ed25519Host.PublicKey()},
	}
	tests := []struct {
		name       string
		serverKeys []gossh.Signer
		hostKey    *NodeHostKey
	}{
		{
			// Go prefers ECDSA host keys, so the server would present its
			// ECDSA key unless the client asks for the trusted type.
			name:       "Trusted",
			serverKeys: []gossh.Signer{ecdsaHost, ed25519Host},
			hostKey:    &NodeHostKey{Node: "web01", Trusted: trustedKeys{"web01": ed25519Host.PublicKey()}},
		},
		{
			name:       "OutdatedPinBesideTrusted",
			serverKeys: []gossh.Signer{ecdsaHost, ed25519Host},
			hostKey:    outdatedPin,
		},
		{
			name:       "OutdatedPinOfTypeServerLacks",
			serverKeys: []gossh.Signer{ed25519Host},
			hostKey:    outdatedPin,
		},
		{
			name:       "OutdatedTrustBesidePin",
			serverKeys: []gossh.Signer{ecdsaHost, ed25519Host},
			hostKey: &NodeHostKey{
				Node: "web01", Pinned: authorizedKey(ed25519Host.PublicKey()),
				Trusted: trustedKeys{"web01": outdatedECDSA},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := startHandshakeServer(t, tt.serverKeys...)

			err := connect(t, &Config{Host: server.host, Port: server.port, NodeHostKey: tt.hostKey})

			require.NoError(t, err)
			assert.EqualValues(t, 1, server.authAttempts.Load(), "only the accepted handshake sends a credential")
		})
	}
}

// Through a bastion, the target is tried once per type of its expected keys
// over the same bastion connection.
func TestNodeHostKeyServerWithSeveralKeysThroughBastion(t *testing.T) {
	t.Parallel()

	ed25519Host := newEd25519Signer(t)
	tests := []struct {
		name       string
		serverKeys []gossh.Signer
	}{
		{name: "ServerHoldsBothTypes", serverKeys: []gossh.Signer{newECDSASigner(t), ed25519Host}},
		{name: "ServerHoldsTrustedTypeOnly", serverKeys: []gossh.Signer{ed25519Host}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			target := startHandshakeServer(t, tt.serverKeys...)
			bastion := startTestServer(t)
			trusted := trustedKeys{"jump": bastion.HostKey, "web01": ed25519Host.PublicKey()}

			err := connect(t, &Config{
				Host: target.host,
				Port: target.port,
				NodeHostKey: &NodeHostKey{
					Node: "web01", Pinned: authorizedKey(newECDSASigner(t).PublicKey()), Trusted: trusted,
				},
				Bastion: &BastionConfig{
					Host:        "127.0.0.1",
					Port:        bastion.Port,
					NodeHostKey: &NodeHostKey{Node: "jump", Trusted: trusted},
				},
			})

			require.NoError(t, err)
			assert.Equal(t, 1, bastion.AuthAttempts())
			assert.EqualValues(t, 1, target.authAttempts.Load(), "only the accepted handshake sends a credential")
		})
	}
}

// A server that holds no key of an expected type is refused as changed,
// naming the node and the algorithms it offers, before any credential.
func TestNodeHostKeyServerHoldsNoExpectedType(t *testing.T) {
	t.Parallel()

	server := startHandshakeServer(t, newECDSASigner(t))
	trusted := newPublicKey(t)

	err := connect(t, &Config{
		Host: server.host, Port: server.port,
		NodeHostKey: &NodeHostKey{Node: "web01", Trusted: trustedKeys{"web01": trusted}},
	})

	hostKeyErr := requireHostKeyError(t, err)
	assert.Equal(t, "web01", hostKeyErr.node)
	assert.Empty(t, hostKeyErr.presented)
	assert.Equal(t, []string{gossh.KeyAlgoECDSA256}, hostKeyErr.offered)
	assert.Equal(t, []string{gossh.FingerprintSHA256(trusted)}, hostKeyErr.expected)
	assert.Zero(t, server.authAttempts.Load(), "no credential may reach the host")
}

// A pinned RSA host certificate is accepted from a server that signs with
// SHA-2 only, as servers with SHA-1 disabled do.
func TestNodeHostKeyRSACertificateWithoutSHA1(t *testing.T) {
	t.Parallel()

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	rsaSigner, err := gossh.NewSignerFromKey(rsaKey)
	require.NoError(t, err)
	sha2Only, err := gossh.NewSignerWithAlgorithms(rsaSigner.(gossh.AlgorithmSigner), []string{gossh.KeyAlgoRSASHA256, gossh.KeyAlgoRSASHA512})
	require.NoError(t, err)
	cert := &gossh.Certificate{
		Key:             rsaSigner.PublicKey(),
		CertType:        gossh.HostCert,
		ValidPrincipals: []string{"web01"},
		ValidBefore:     gossh.CertTimeInfinity,
	}
	require.NoError(t, cert.SignCert(rand.Reader, newEd25519Signer(t)))
	certSigner, err := gossh.NewCertSigner(cert, sha2Only)
	require.NoError(t, err)
	server := startHandshakeServer(t, certSigner)

	err = connect(t, &Config{
		Host: server.host, Port: server.port,
		NodeHostKey: &NodeHostKey{Node: "web01", Pinned: authorizedKey(cert)},
	})

	require.NoError(t, err)
}

func newEd25519Signer(t *testing.T) gossh.Signer {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := gossh.NewSignerFromKey(key)
	require.NoError(t, err)
	return signer
}

func newECDSASigner(t *testing.T) gossh.Signer {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	signer, err := gossh.NewSignerFromKey(key)
	require.NoError(t, err)
	return signer
}

// handshakeServer holds the given host keys, accepts the test password, and
// counts the credentials it receives. The harness server holds one key of
// one type, which these tests need more of.
type handshakeServer struct {
	host, port   string
	authAttempts atomic.Int32
}

func startHandshakeServer(t *testing.T, hostKeys ...gossh.Signer) *handshakeServer {
	t.Helper()

	server := &handshakeServer{}
	config := &gossh.ServerConfig{
		PasswordCallback: func(meta gossh.ConnMetadata, password []byte) (*gossh.Permissions, error) {
			server.authAttempts.Add(1)
			if meta.User() == testUser && string(password) == testPassword {
				return nil, nil
			}
			return nil, errors.New("password rejected")
		},
	}
	for _, key := range hostKeys {
		config.AddHostKey(key)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	server.host, server.port, err = net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serveHandshake(conn, config)
		}
	}()
	return server
}

// serveHandshake completes the handshake and accepts session channels, so a
// client can open a session, and runs nothing.
func serveHandshake(conn net.Conn, config *gossh.ServerConfig) {
	defer func() { _ = conn.Close() }()
	server, channels, requests, err := gossh.NewServerConn(conn, config)
	if err != nil {
		return
	}
	defer func() { _ = server.Close() }()
	go gossh.DiscardRequests(requests)
	for newChannel := range channels {
		channel, channelRequests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go gossh.DiscardRequests(channelRequests)
		_ = channel.Close()
	}
}
