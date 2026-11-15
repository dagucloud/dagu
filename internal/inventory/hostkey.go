// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package inventory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// ErrNoTrustedHostKey reports a node for which no host key is trusted.
var ErrNoTrustedHostKey = errors.New("no trusted host key")

// HostKeyStore keeps a project's trusted host keys, at most one per node.
// Node names are compared without case.
type HostKeyStore interface {
	// Get returns the key trusted for node, or ErrNoTrustedHostKey.
	Get(ctx context.Context, node string) (TrustedHostKey, error)
	// Trust stores key as the trusted key of node, replacing the key trusted
	// before. by names the user who trusts it.
	Trust(ctx context.Context, node string, key ssh.PublicKey, by string) (TrustedHostKey, error)
}

// TrustedHostKey is a host key a user trusted for a node, which a connection
// with strict host key checking then accepts (RFC 012 §Host keys).
type TrustedHostKey struct {
	// Node is the name of the node, as it was trusted.
	Node string
	// Key is the public key in authorized_keys form, such as ssh-ed25519 AAAA….
	Key string
	// Fingerprint is the key's SHA256 fingerprint, as ssh-keygen -l prints it.
	Fingerprint string
	// TrustedBy names the user who trusted the key.
	TrustedBy string
	TrustedAt time.Time
}

// NewTrustedHostKey describes key as trusted for node by a user at a time.
// It fails when node cannot name a node.
func NewTrustedHostKey(node string, key ssh.PublicKey, by string, at time.Time) (TrustedHostKey, error) {
	if err := nodeNameError(node); err != nil {
		return TrustedHostKey{}, fmt.Errorf("%w: node name: %w", ErrInvalid, err)
	}
	if key == nil {
		return TrustedHostKey{}, fmt.Errorf("trusting a host key of node %q: the key is missing", node)
	}
	return TrustedHostKey{
		Node:        node,
		Key:         strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))),
		Fingerprint: ssh.FingerprintSHA256(key),
		TrustedBy:   by,
		TrustedAt:   at,
	}, nil
}

// PublicKey parses Key.
func (k TrustedHostKey) PublicKey() (ssh.PublicKey, error) {
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k.Key))
	if err != nil {
		return nil, fmt.Errorf("trusted host key of node %q: %w", k.Node, err)
	}
	return key, nil
}
