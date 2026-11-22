// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ssh

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// nodeConfig returns a password connection to the node web01 at port, with
// its host key checked against trusted.
func nodeConfig(port string, trusted trustedKeys) Config {
	return Config{
		Host:        "127.0.0.1",
		Port:        port,
		User:        testUser,
		SSHAuth:     ir.SSHAuth{Password: testPassword},
		Timeout:     10 * time.Second,
		NodeHostKey: &NodeHostKey{Node: "web01", Trusted: trusted},
	}
}

// throughBastion adds the bastion node jump at port to cfg.
func throughBastion(cfg Config, port string, trusted trustedKeys) Config {
	cfg.Bastion = &BastionConfig{
		Host:        "127.0.0.1",
		Port:        port,
		User:        testUser,
		SSHAuth:     ir.SSHAuth{Password: testPassword},
		NodeHostKey: &NodeHostKey{Node: "jump", Trusted: trusted},
	}
	return cfg
}

// A test reports the fingerprint each hop presented, and runs true once it
// has authenticated.
func TestConnectionReportsEachHop(t *testing.T) {
	t.Parallel()

	bastion := startTestServer(t)
	target := startTestServer(t)
	trusted := trustedKeys{"jump": bastion.HostKey, "web01": target.HostKey}

	test, err := TestConnection(context.Background(), throughBastion(nodeConfig(target.Port, trusted), bastion.Port, trusted))
	require.NoError(t, err)

	assert.Equal(t, gossh.FingerprintSHA256(target.HostKey), test.Fingerprint)
	assert.Equal(t, gossh.FingerprintSHA256(bastion.HostKey), test.BastionFingerprint)
	assert.Equal(t, []string{testCommand}, target.Commands())
}

// A test that meets an unknown key reports the hop and the key it presented,
// and sends that hop no credential.
func TestConnectionRefusesUnknownKey(t *testing.T) {
	t.Parallel()

	bastion := startTestServer(t)
	target := startTestServer(t)
	trusted := trustedKeys{"jump": bastion.HostKey}

	test, err := TestConnection(context.Background(), throughBastion(nodeConfig(target.Port, trusted), bastion.Port, trusted))

	refused, ok := AsHostKeyRefusal(err)
	require.True(t, ok, "want a host key refusal, got %v", err)
	assert.Equal(t, HostKeyRefusal{Hop: hopNode, Node: "web01", Presented: gossh.FingerprintSHA256(target.HostKey)}, refused)
	assert.Equal(t, refused.Presented, test.Fingerprint)
	assert.Zero(t, target.AuthAttempts(), "credentials sent to the target")
	assert.Empty(t, target.Commands())
}

// FindHostKey finds whichever of a server's keys has the fingerprint, and
// sends the server no credential.
func TestFindHostKey(t *testing.T) {
	t.Parallel()

	ed25519Host := newEd25519Signer(t)
	ecdsaHost := newECDSASigner(t)
	server := startHandshakeServer(t, ecdsaHost, ed25519Host)
	cfg := nodeConfig(server.port, nil)

	for _, want := range []gossh.PublicKey{ed25519Host.PublicKey(), ecdsaHost.PublicKey()} {
		key, err := FindHostKey(context.Background(), cfg, gossh.FingerprintSHA256(want))
		require.NoError(t, err)
		assert.Equal(t, want.Marshal(), key.Marshal())
	}

	_, err := FindHostKey(context.Background(), cfg, gossh.FingerprintSHA256(newPublicKey(t)))
	require.Error(t, err)
	assert.Contains(t, err.Error(), gossh.FingerprintSHA256(ed25519Host.PublicKey()))
	assert.Contains(t, err.Error(), gossh.FingerprintSHA256(ecdsaHost.PublicKey()))
	assert.Zero(t, server.authAttempts.Load(), "credentials sent to the server")
}

// Through a bastion, FindHostKey checks the bastion's key before it sends
// the bastion its credentials.
func TestFindHostKeyThroughBastion(t *testing.T) {
	t.Parallel()

	targetHost := newEd25519Signer(t)
	target := startHandshakeServer(t, targetHost)
	targetKey := gossh.FingerprintSHA256(targetHost.PublicKey())
	t.Cleanup(func() { assert.Zero(t, target.authAttempts.Load(), "credentials sent to the target") })

	t.Run("BastionTrusted", func(t *testing.T) {
		t.Parallel()
		bastion := startTestServer(t)
		trusted := trustedKeys{"jump": bastion.HostKey}

		key, err := FindHostKey(context.Background(), throughBastion(nodeConfig(target.port, trusted), bastion.Port, trusted), targetKey)
		require.NoError(t, err)
		assert.Equal(t, targetKey, gossh.FingerprintSHA256(key))
		assert.Equal(t, 1, bastion.AuthAttempts())
	})
	t.Run("BastionUnknown", func(t *testing.T) {
		t.Parallel()
		bastion := startTestServer(t)

		_, err := FindHostKey(context.Background(), throughBastion(nodeConfig(target.port, nil), bastion.Port, nil), targetKey)
		refused, ok := AsHostKeyRefusal(err)
		require.True(t, ok, "want a host key refusal, got %v", err)
		assert.Equal(t, hopBastion, refused.Hop)
		assert.Equal(t, "jump", refused.Node)
		assert.Zero(t, bastion.AuthAttempts(), "credentials sent to the bastion")
	})
}
