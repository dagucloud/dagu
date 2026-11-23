// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sshconn

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// testCommand is what a connection test runs once it has authenticated.
const testCommand = "true"

// ConnectionTest is what a connection test saw of each hop.
type ConnectionTest struct {
	// Fingerprint is the SHA256 fingerprint of the host key the target
	// presented, or empty when the test did not reach it.
	Fingerprint string
	// BastionFingerprint is the one the bastion presented, or empty without
	// a bastion.
	BastionFingerprint string
}

// TestConnection connects as cfg describes, checking each hop's host key and
// authenticating, and runs true (RFC 012 §Host keys). A hop whose host key
// is refused receives no credential, and the error then holds a
// [HostKeyRefusal].
func TestConnection(ctx context.Context, cfg Config) (ConnectionTest, error) {
	var test ConnectionTest
	client, err := NewClient(ctx, &cfg)
	if err != nil {
		return test, err
	}
	client.cfg.HostKeyCallback = recordFingerprint(client.cfg.HostKeyCallback, &test.Fingerprint)
	if client.bastionCfg != nil {
		client.bastionCfg.cfg.HostKeyCallback = recordFingerprint(client.bastionCfg.cfg.HostKeyCallback, &test.BastionFingerprint)
	}
	conn, session, err := client.NewSession(ctx)
	if err != nil {
		return test, err
	}
	defer func() { _ = conn.Close() }()
	defer func() { _ = session.Close() }()
	// true runs as a run's command step runs: through the node's shell, or
	// DefaultShell, reading its script from stdin.
	shell, shellArgs := client.Shell, client.ShellArgs
	if shell == "" {
		shell, shellArgs = DefaultShell, nil
	}
	command := ShellCommand(shell, shellArgs)
	session.Stdin = strings.NewReader(Script("set -e\n" + testCommand + "\n"))
	if err := session.Run(command); err != nil {
		return test, fmt.Errorf("running %s with %s on %s: %w", testCommand, command, client.hostPort, err)
	}
	return test, nil
}

// recordFingerprint returns callback, noting the fingerprint of each key a
// server presents to it.
func recordFingerprint(callback ssh.HostKeyCallback, fingerprint *string) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		*fingerprint = ssh.FingerprintSHA256(key)
		return callback(hostname, remote, key)
	}
}

// errHostKeyRead ends a handshake once the server has presented its key, so
// no credential is sent.
var errHostKeyRead = errors.New("host key read")

// hostKeyAlgorithmSets offer one key type per handshake, so a server
// presents each key it holds in turn, as keyAlgorithms lists them.
var hostKeyAlgorithmSets = [][]string{
	{ssh.KeyAlgoED25519},
	{ssh.KeyAlgoECDSA256},
	{ssh.KeyAlgoECDSA384},
	{ssh.KeyAlgoECDSA521},
	{ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSA},
}

// FindHostKey returns the host key with fingerprint that the target cfg
// describes holds. The target receives no credential: each handshake ends
// once the server presents its key. A bastion is reached as a connection
// reaches it, with its host key checked and its credentials sent.
func FindHostKey(ctx context.Context, cfg Config, fingerprint string) (ssh.PublicKey, error) {
	timeout := defaultIfZero(cfg.Timeout, DefaultTimeout)
	hostPort := net.JoinHostPort(cfg.Host, defaultIfEmpty(cfg.Port, "22"))
	var bastion *ssh.Client
	if cfg.Bastion != nil {
		var err error
		if bastion, err = dialBastion(ctx, &cfg, timeout); err != nil {
			return nil, err
		}
		defer func() { _ = bastion.Close() }()
	}

	// The first handshake offers the client's default algorithms, as a test
	// of a node with no expected key does, so the key a test reported comes
	// back first. Each later one asks for a type not yet presented.
	var presented []ssh.PublicKey
	for _, algorithms := range slices.Concat([][]string{nil}, hostKeyAlgorithmSets) {
		if slices.ContainsFunc(presented, func(key ssh.PublicKey) bool { return slices.Equal(keyAlgorithms(key), algorithms) }) {
			continue
		}
		key, err := presentedKey(ctx, hostPort, cfg.User, timeout, algorithms, bastion)
		if negotiation, ok := errors.AsType[*ssh.AlgorithmNegotiationError](err); ok && negotiation.What == hostKeyNegotiation {
			continue // The server holds no key of this type.
		}
		if err != nil {
			return nil, err
		}
		if ssh.FingerprintSHA256(key) == fingerprint {
			return key, nil
		}
		if !slices.ContainsFunc(presented, func(k ssh.PublicKey) bool { return bytes.Equal(k.Marshal(), key.Marshal()) }) {
			presented = append(presented, key)
		}
	}
	fingerprints := make([]string, 0, len(presented))
	for _, key := range presented {
		fingerprints = append(fingerprints, ssh.FingerprintSHA256(key))
	}
	return nil, fmt.Errorf("%s holds no host key with fingerprint %s; it presents %s", hostPort, fingerprint, strings.Join(fingerprints, ", "))
}

// dialBastion connects to the bastion of cfg, checking its host key.
func dialBastion(ctx context.Context, cfg *Config, timeout time.Duration) (*ssh.Client, error) {
	target, err := targetHostKeyPolicy(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to setup host key verification: %w", err)
	}
	hostKey, err := bastionHostKeyPolicy(ctx, cfg, target)
	if err != nil {
		return nil, fmt.Errorf("failed to setup bastion host key verification: %w", err)
	}
	bastionCfg, err := newBastionClientConfig(ctx, cfg.Bastion, timeout, hostKey)
	if err != nil {
		return nil, fmt.Errorf("failed to setup bastion client: %w", err)
	}
	client, err := hostKey.handshake(ctx, bastionCfg.cfg, func(c *ssh.ClientConfig) (*ssh.Client, error) {
		return dialContext(ctx, bastionCfg.hostPort, c)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to bastion host: %w", err)
	}
	return client, nil
}

// presentedKey runs one handshake with hostPort, through bastion when it is
// set, offering algorithms, and returns the key the server presents.
func presentedKey(ctx context.Context, hostPort, user string, timeout time.Duration, algorithms []string, bastion *ssh.Client) (ssh.PublicKey, error) {
	var key ssh.PublicKey
	cfg := &ssh.ClientConfig{
		User:              user,
		Timeout:           timeout,
		HostKeyAlgorithms: algorithms,
		HostKeyCallback: func(_ string, _ net.Addr, presented ssh.PublicKey) error {
			key = presented
			return errHostKeyRead
		},
	}
	var err error
	if bastion == nil {
		var client *ssh.Client
		if client, err = dialContext(ctx, hostPort, cfg); client != nil {
			_ = client.Close()
		}
	} else {
		conn, dialErr := bastion.Dial("tcp", hostPort)
		if dialErr != nil {
			return nil, fmt.Errorf("failed to dial target through bastion: %w", dialErr)
		}
		_, _, _, err = ssh.NewClientConn(conn, hostPort, cfg)
		_ = conn.Close()
	}
	if key != nil {
		return key, nil
	}
	if err == nil {
		err = fmt.Errorf("%s presented no host key", hostPort)
	}
	return nil, err
}

// HostKeyRefusal describes a host key that a hop of a node connection
// refused.
type HostKeyRefusal struct {
	// Hop is "node" or "bastion".
	Hop string
	// Node names the node whose key was refused: the target, or the node
	// that serves as its bastion.
	Node string
	// Presented is the fingerprint of the key the server presented. It is
	// empty when the server presented none.
	Presented string
	// Expected are the fingerprints of the keys the node may present. None
	// means the key is unknown, and some mean it has changed.
	Expected []string
	// Trusted is the fingerprint among Expected of the key trusted for the
	// node, or empty when none is.
	Trusted string
}

// AsHostKeyRefusal returns the host key refusal that err holds.
func AsHostKeyRefusal(err error) (HostKeyRefusal, bool) {
	refused, ok := errors.AsType[*hostKeyError](err)
	if !ok {
		return HostKeyRefusal{}, false
	}
	return HostKeyRefusal{
		Hop:       refused.hop,
		Node:      refused.node,
		Presented: refused.presented,
		Expected:  slices.Clone(refused.expected),
		Trusted:   refused.trusted,
	}, true
}
