// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ssh

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"net"
	"strings"
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

// A strict node connection refuses a key that is neither the node's pinned
// key nor its trusted key, and the server sees no credential.
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
				return &NodeHostKey{Node: "web01", Strict: true, Trusted: trustedKeys{}}
			},
		},
		{
			name: "TrustedKeyChanged",
			hostKey: func(*harness.SSHServer) *NodeHostKey {
				return &NodeHostKey{Node: "web01", Strict: true, Trusted: trustedKeys{"web01": other}}
			},
			expected: []string{gossh.FingerprintSHA256(other)},
		},
		{
			name: "PinnedKeyChanged",
			hostKey: func(*harness.SSHServer) *NodeHostKey {
				return &NodeHostKey{Node: "web01", Strict: true, Pinned: authorizedKey(other)}
			},
			expected: []string{gossh.FingerprintSHA256(other)},
		},
		{
			name: "KeyTrustedForAnotherNode",
			hostKey: func(server *harness.SSHServer) *NodeHostKey {
				return &NodeHostKey{Node: "web01", Strict: true, Trusted: trustedKeys{"web02": server.HostKey}}
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
				return &NodeHostKey{Node: "web01", Strict: true, Pinned: authorizedKey(server.HostKey)}
			},
		},
		{
			name: "Trusted",
			hostKey: func(server *harness.SSHServer) *NodeHostKey {
				return &NodeHostKey{Node: "web01", Strict: true, Trusted: trustedKeys{"web01": server.HostKey}}
			},
		},
		{
			// The node's host_key is outdated, and the key trusted since is
			// accepted.
			name: "TrustedBesidePinned",
			hostKey: func(server *harness.SSHServer) *NodeHostKey {
				return &NodeHostKey{
					Node: "web01", Strict: true, Pinned: authorizedKey(newPublicKey(t)),
					Trusted: trustedKeys{"web01": server.HostKey},
				}
			},
		},
		{
			name: "NotStrict",
			hostKey: func(*harness.SSHServer) *NodeHostKey {
				return &NodeHostKey{Node: "web01", Trusted: trustedKeys{}}
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
				NodeHostKey: &NodeHostKey{Node: "web01", Strict: true, Trusted: trusted},
				Bastion: &BastionConfig{
					Host:        "127.0.0.1",
					Port:        bastion.Port,
					NodeHostKey: &NodeHostKey{Node: "jump", Strict: true, Trusted: trusted},
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
		NodeHostKey: &NodeHostKey{Node: "web01", Strict: true},
		Bastion:     &BastionConfig{Host: "127.0.0.1", SSHAuth: ir.SSHAuth{Password: testPassword}},
	})

	assert.ErrorContains(t, err, `the bastion of node "web01" names no node`)
}

// A server with host keys of several types presents the type of the key that
// is trusted, rather than the type the client prefers by default.
func TestNodeHostKeyServerWithSeveralKeys(t *testing.T) {
	t.Parallel()

	addr, ed25519Key := startServerWithSeveralHostKeys(t)
	host, port, err := net.SplitHostPort(addr)
	require.NoError(t, err)

	err = connect(t, &Config{
		Host: host, Port: port,
		NodeHostKey: &NodeHostKey{Node: "web01", Strict: true, Trusted: trustedKeys{"web01": ed25519Key}},
	})

	require.NoError(t, err)
}

// startServerWithSeveralHostKeys starts a server that holds an ECDSA and an
// Ed25519 host key, as OpenSSH servers commonly do, and accepts the test
// password. It returns its address and the Ed25519 key.
func startServerWithSeveralHostKeys(t *testing.T) (string, gossh.PublicKey) {
	t.Helper()

	config := &gossh.ServerConfig{
		PasswordCallback: func(meta gossh.ConnMetadata, password []byte) (*gossh.Permissions, error) {
			if meta.User() == testUser && string(password) == testPassword {
				return nil, nil
			}
			return nil, errors.New("password rejected")
		},
	}
	ecdsaPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	ecdsaSigner, err := gossh.NewSignerFromKey(ecdsaPriv)
	require.NoError(t, err)
	config.AddHostKey(ecdsaSigner)
	_, edPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	edSigner, err := gossh.NewSignerFromKey(edPriv)
	require.NoError(t, err)
	config.AddHostKey(edSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				server, channels, requests, err := gossh.NewServerConn(conn, config)
				if err != nil {
					return
				}
				defer func() { _ = server.Close() }()
				go gossh.DiscardRequests(requests)
				for ch := range channels {
					channel, requests, err := ch.Accept()
					if err != nil {
						continue
					}
					go gossh.DiscardRequests(requests)
					_ = channel.Close()
				}
			}()
		}
	}()
	return listener.Addr().String(), edSigner.PublicKey()
}
