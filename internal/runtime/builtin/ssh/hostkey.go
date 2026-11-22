// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ssh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/inventory"
)

// NodeHostKey names the inventory node a hop connects to, so the hop accepts
// only the host keys of that node (RFC 012 §Host keys).
type NodeHostKey struct {
	// Node is the node's name.
	Node string
	// Pinned is the node's host_key in authorized_keys form, or empty.
	Pinned string
	// AcceptAnyKey accepts whatever key the server presents, as
	// strict_host_key: false asks. Otherwise a key that is neither pinned nor
	// trusted is refused.
	AcceptAnyKey bool
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

// hostKeyNegotiation is what x/crypto names a failure to agree on a host
// key algorithm (ssh.AlgorithmNegotiationError).
const hostKeyNegotiation = "host key"

// hostKeyPolicy is how one hop checks the key its server presents.
type hostKeyPolicy struct {
	callback ssh.HostKeyCallback
	// check, when set, is the node check behind callback. Its expected keys
	// choose the host key algorithms each handshake offers.
	check *hostKeyCheck
}

// handshake runs dial with cfg until a host key is accepted, offering the
// algorithms of one type of expected key per handshake. A server presents
// one host key per handshake, of the first offered type it holds, so a node
// whose expected keys differ in type is tried once per type. A handshake
// that fails on the host key has sent no credential, so the next type is
// safe to try. When a later type is accepted, the expected keys of the types
// before it are reported as out of date.
func (p hostKeyPolicy) handshake(ctx context.Context, cfg *ssh.ClientConfig, dial func(*ssh.ClientConfig) (*ssh.Client, error)) (*ssh.Client, error) {
	var sets [][]string
	if p.check != nil {
		sets = p.check.algorithmSets()
	}
	if len(sets) == 0 {
		return dial(cfg)
	}
	var refused error
	var offered []string
	for i, algorithms := range sets {
		attempt := *cfg
		attempt.HostKeyAlgorithms = algorithms
		client, err := dial(&attempt)
		if err == nil {
			if i > 0 {
				p.check.warnOutdated(ctx, sets[:i])
			}
			return client, nil
		}
		if _, ok := errors.AsType[*hostKeyError](err); ok {
			if refused == nil {
				refused = err
			}
			continue
		}
		if negotiation, ok := errors.AsType[*ssh.AlgorithmNegotiationError](err); ok && negotiation.What == hostKeyNegotiation {
			// The server holds no key of this type.
			offered = negotiation.RequestedAlgorithms
			continue
		}
		return nil, err
	}
	if refused != nil {
		return nil, refused
	}
	return nil, p.check.refusal("", offered)
}

// policy reads the keys the node accepts. A server presents a key before
// the client sends any credential, so a key the policy refuses ends the
// connection with no credential sent.
func (k *NodeHostKey) policy(ctx context.Context, hop string) (hostKeyPolicy, error) {
	if k.AcceptAnyKey {
		return hostKeyPolicy{callback: ssh.InsecureIgnoreHostKey()}, nil //nolint:gosec // strict_host_key: false opts out.
	}
	check := &hostKeyCheck{hop: hop, node: k.Node}
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
	return hostKeyPolicy{callback: check.verify, check: check}, nil
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
	return c.refusal(ssh.FingerprintSHA256(presented), nil)
}

// refusal reports the key the server presented, or, when it presented
// none, the host key algorithms it offers.
func (c hostKeyCheck) refusal(presented string, offered []string) error {
	err := &hostKeyError{hop: c.hop, node: c.node, presented: presented, offered: offered}
	for _, key := range c.expected {
		err.expected = append(err.expected, ssh.FingerprintSHA256(key))
	}
	return err
}

// algorithmSets lists, for each type of expected key, the host key
// algorithms that prove a key of that type. With no expected key, a single
// handshake offers every algorithm, so the error can name whatever key the
// server presents.
func (c hostKeyCheck) algorithmSets() [][]string {
	var sets [][]string
	for _, key := range c.expected {
		algorithms := keyAlgorithms(key)
		if !slices.ContainsFunc(sets, func(set []string) bool { return slices.Equal(set, algorithms) }) {
			sets = append(sets, algorithms)
		}
	}
	return sets
}

// warnOutdated reports the expected keys of the tried algorithm sets, which
// the server does not hold. Until the node's host_key or trusted key is
// updated, each connection to it pays an extra handshake, and the server
// logs a failed one.
func (c hostKeyCheck) warnOutdated(ctx context.Context, tried [][]string) {
	var outdated []string
	for _, key := range c.expected {
		algorithms := keyAlgorithms(key)
		if slices.ContainsFunc(tried, func(set []string) bool { return slices.Equal(set, algorithms) }) {
			outdated = append(outdated, ssh.FingerprintSHA256(key))
		}
	}
	logger.Warn(ctx, "Server does not hold an expected host key; update the node's host_key or trust its current key",
		slog.String("hop", c.hop),
		slog.String("node", c.node),
		slog.String("outdated", strings.Join(outdated, ", ")),
	)
}

// keyAlgorithms lists the signature algorithms for a key, as known_hosts
// checking offers them. An RSA key, and an RSA certificate, sign with SHA-2
// as well as SHA-1.
func keyAlgorithms(key ssh.PublicKey) []string {
	switch key.Type() {
	case ssh.KeyAlgoRSA:
		return []string{ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSA}
	case ssh.CertAlgoRSAv01:
		return []string{ssh.CertAlgoRSASHA256v01, ssh.CertAlgoRSASHA512v01, ssh.CertAlgoRSAv01}
	default:
		return []string{key.Type()}
	}
}

// hostKeyError reports a host key that a node connection refused.
type hostKeyError struct {
	hop  string
	node string
	// presented is the fingerprint of the key the server presented. It is
	// empty when the server holds no key of an expected type, and offered
	// lists the host key algorithms it offers instead.
	presented string
	offered   []string
	// expected are the fingerprints of the keys the node may present. None
	// means the key is unknown, and some means it has changed.
	expected []string
}

func (e *hostKeyError) Error() string {
	switch {
	case len(e.expected) == 0:
		return fmt.Sprintf("host key of %s %q is unknown: it presented %s", e.hop, e.node, e.presented)
	case e.presented == "":
		return fmt.Sprintf("host key of %s %q has changed: it holds no key of an expected type and offers %s, expected %s",
			e.hop, e.node, strings.Join(e.offered, ", "), strings.Join(e.expected, " or "))
	default:
		return fmt.Sprintf("host key of %s %q has changed: it presented %s, expected %s",
			e.hop, e.node, e.presented, strings.Join(e.expected, " or "))
	}
}
