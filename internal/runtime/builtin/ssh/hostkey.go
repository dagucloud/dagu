// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ssh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/dagucloud/dagu/v2/internal/inventory"
)

// NodeHostKey names the inventory node a hop connects to, so the hop accepts
// only the host keys of that node (RFC 012 §Host keys).
type NodeHostKey struct {
	// Node is the node's name.
	Node string
	// Pinned is the node's host_key in authorized_keys form, or empty.
	Pinned string
	// Strict refuses a key that is neither pinned nor trusted. Otherwise any
	// key is accepted.
	Strict bool
	// Trusted finds the key a user trusted for the node. Nil trusts none.
	Trusted TrustedHostKeys
}

// TrustedHostKeys finds the host key trusted for a node. A project's
// [inventory.HostKeyStore] implements it.
type TrustedHostKeys interface {
	// Get returns the key trusted for node, or inventory.ErrNoTrustedHostKey.
	Get(ctx context.Context, node string) (inventory.TrustedHostKey, error)
}

// A project's host key store finds trusted keys for node connections.
var _ TrustedHostKeys = inventory.HostKeyStore(nil)

// Hops of a connection, as host key errors name them.
const (
	hopNode    = "node"
	hopBastion = "bastion"
)

// hostKeyPolicy is how one hop checks the key its server presents.
type hostKeyPolicy struct {
	callback ssh.HostKeyCallback
	// algorithms, when set, are the only host key algorithms offered, so a
	// server with keys of several types presents the one that is expected.
	algorithms []string
}

// policy reads the keys the node accepts. A server presents a key before
// the client sends any credential, so a key the policy refuses ends the
// connection with no credential sent.
func (k *NodeHostKey) policy(ctx context.Context, hop string) (hostKeyPolicy, error) {
	if !k.Strict {
		return hostKeyPolicy{callback: ssh.InsecureIgnoreHostKey()}, nil //nolint:gosec // strict_host_key: false opts out.
	}
	check := hostKeyCheck{hop: hop, node: k.Node}
	if k.Pinned != "" {
		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k.Pinned))
		if err != nil {
			return hostKeyPolicy{}, fmt.Errorf("%s %q: host_key: %w", hop, k.Node, err)
		}
		check.expected = append(check.expected, key)
	}
	if k.Trusted != nil {
		trusted, err := k.Trusted.Get(ctx, k.Node)
		switch {
		case errors.Is(err, inventory.ErrNoTrustedHostKey):
			// No key is trusted for the node yet.
		case err != nil:
			return hostKeyPolicy{}, fmt.Errorf("%s %q: reading its trusted host key: %w", hop, k.Node, err)
		default:
			key, err := trusted.PublicKey()
			if err != nil {
				return hostKeyPolicy{}, fmt.Errorf("%s %q: %w", hop, k.Node, err)
			}
			check.expected = append(check.expected, key)
		}
	}
	return hostKeyPolicy{callback: check.verify, algorithms: check.algorithms()}, nil
}

// hostKeyCheck accepts the keys a node is expected to present.
type hostKeyCheck struct {
	hop      string
	node     string
	expected []ssh.PublicKey
}

func (c hostKeyCheck) verify(_ string, _ net.Addr, presented ssh.PublicKey) error {
	for _, key := range c.expected {
		if bytes.Equal(key.Marshal(), presented.Marshal()) {
			return nil
		}
	}
	err := &hostKeyError{hop: c.hop, node: c.node, presented: ssh.FingerprintSHA256(presented)}
	for _, key := range c.expected {
		err.expected = append(err.expected, ssh.FingerprintSHA256(key))
	}
	return err
}

// algorithms lists the host key algorithms that prove the expected keys.
// With no expected key, every algorithm is offered, so the error can name
// whatever key the server presents.
func (c hostKeyCheck) algorithms() []string {
	var algos []string
	for _, key := range c.expected {
		for _, algo := range keyAlgorithms(key) {
			if !slices.Contains(algos, algo) {
				algos = append(algos, algo)
			}
		}
	}
	return algos
}

// keyAlgorithms lists the signature algorithms for a key, as known_hosts
// checking offers them. An RSA key signs with SHA-2 as well as SHA-1.
func keyAlgorithms(key ssh.PublicKey) []string {
	if key.Type() == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSA}
	}
	return []string{key.Type()}
}

// hostKeyError reports a host key that a node connection refused.
type hostKeyError struct {
	hop       string
	node      string
	presented string
	// expected are the fingerprints of the keys the node may present. None
	// means the key is unknown, and some means it has changed.
	expected []string
}

func (e *hostKeyError) Error() string {
	if len(e.expected) == 0 {
		return fmt.Sprintf("host key of %s %q is unknown: it presented %s", e.hop, e.node, e.presented)
	}
	return fmt.Sprintf("host key of %s %q has changed: it presented %s, expected %s",
		e.hop, e.node, e.presented, strings.Join(e.expected, " or "))
}
