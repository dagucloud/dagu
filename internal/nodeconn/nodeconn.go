// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package nodeconn tests connections to inventory nodes and trusts their host
// keys from the server (RFC 012 §Host keys).
package nodeconn

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
	"github.com/dagucloud/dagu/v2/internal/sshconn"
)

const (
	// maxConnections bounds the nodes a test or trust reaches at once.
	maxConnections = 16
	// nodeTimeout bounds the time one node's test or key read takes.
	nodeTimeout = time.Minute
)

// SecretResolver resolves the secret refs of node credentials.
type SecretResolver interface {
	ResolveReference(ctx context.Context, ref secretref.Ref) (string, error)
}

// Service tests and trusts the nodes of a project's inventories.
type Service struct {
	hostKeys inventory.HostKeyStore
	secrets  SecretResolver
}

// New returns a service that checks host keys against hostKeys, stores
// trusted keys there, and reads node credentials through secrets. A nil
// secrets fails every node whose credentials are secret refs.
func New(hostKeys inventory.HostKeyStore, secrets SecretResolver) *Service {
	return &Service{hostKeys: hostKeys, secrets: secrets}
}

// TestResult is the outcome of testing one node.
type TestResult struct {
	Node string
	// Fingerprint and BastionFingerprint are those of the host keys the
	// node and its bastion presented, as far as the test reached.
	Fingerprint        string
	BastionFingerprint string
	Duration           time.Duration
	// Err is why the test failed, or nil when it passed.
	Err error
	// Refused describes the host key that ended the test, when one did.
	Refused *sshconn.HostKeyRefusal
}

// Test tests each of targets, finding bastions among nodes: it checks each
// hop's host key, authenticates, and runs true. It calls emit once per
// target as each finishes, never concurrently, and returns when all have
// finished. A test never trusts a key.
func (s *Service) Test(ctx context.Context, nodes, targets []inventory.ResolvedNode, emit func(TestResult)) {
	secrets := s.newSecretCache()
	forEach(ctx, targets, emit, func(ctx context.Context, node inventory.ResolvedNode) TestResult {
		started := time.Now()
		test, err := s.test(ctx, nodes, node, secrets)
		result := TestResult{
			Node:               node.Name,
			Fingerprint:        test.Fingerprint,
			BastionFingerprint: test.BastionFingerprint,
			Duration:           time.Since(started),
			Err:                err,
		}
		if refused, ok := sshconn.AsHostKeyRefusal(err); ok {
			result.Refused = &refused
		}
		return result
	})
}

func (s *Service) test(ctx context.Context, nodes []inventory.ResolvedNode, node inventory.ResolvedNode, secrets *secretCache) (sshconn.ConnectionTest, error) {
	bastion, err := bastionOf(nodes, node)
	if err != nil {
		return sshconn.ConnectionTest{}, err
	}
	cfg, err := sshconn.NodeConfig(node, bastion, s.hostKeys)
	if err != nil {
		return sshconn.ConnectionTest{}, err
	}
	values, err := secrets.resolve(ctx, sshconn.NodeSecretRefs(node, bastion))
	if err != nil {
		return sshconn.ConnectionTest{}, err
	}
	return sshconn.TestConnection(sshconn.WithSecretValues(ctx, values), cfg)
}

// TrustRequest asks to trust the host key of Node whose fingerprint the user
// saw. Replace is the fingerprint of the trusted key the user saw and agreed
// to replace, or empty to replace none.
type TrustRequest struct {
	Node        string
	Fingerprint string
	Replace     string
}

// TrustOutcome is what a trust request did.
type TrustOutcome string

const (
	// TrustOutcomeTrusted stores a key for a node that had none trusted.
	TrustOutcomeTrusted TrustOutcome = "trusted"
	// TrustOutcomeReplaced stores a key in place of the one trusted before.
	TrustOutcomeReplaced TrustOutcome = "replaced"
	// TrustOutcomeUnchanged finds the key already trusted.
	TrustOutcomeUnchanged TrustOutcome = "unchanged"
)

// TrustResult is the outcome of one trust request.
type TrustResult struct {
	Node        string
	Fingerprint string
	// Previous is the fingerprint of another key trusted before, when one
	// was.
	Previous string
	// Outcome is empty when Err is set.
	Outcome TrustOutcome
	Err     error
}

// Trust trusts, for each request, the key its node presents when that key
// has the fingerprint the user saw, finding nodes and bastions among nodes.
// The node receives no credential; a bastion is reached with its host key
// checked. A request stores its key only when Replace names the key trusted
// for the node, or is empty while none is. Trust calls emit once per request
// as each finishes, never concurrently, and returns when all have finished.
func (s *Service) Trust(ctx context.Context, nodes []inventory.ResolvedNode, requests []TrustRequest, by string, emit func(TrustResult)) {
	secrets := s.newSecretCache()
	forEach(ctx, requests, emit, func(ctx context.Context, request TrustRequest) TrustResult {
		result := TrustResult{Node: request.Node, Fingerprint: request.Fingerprint}
		node, ok := findNode(nodes, request.Node)
		if !ok {
			result.Err = fmt.Errorf("node %q is not in the project's inventories", request.Node)
			return result
		}
		result.Node = node.Name
		result.Outcome, result.Err = s.trust(ctx, nodes, node, request, by, secrets, &result.Previous)
		return result
	})
}

// trust stores the key node presents with the requested fingerprint, and
// sets previous to the fingerprint of another key trusted before.
func (s *Service) trust(ctx context.Context, nodes []inventory.ResolvedNode, node inventory.ResolvedNode, request TrustRequest, by string, secrets *secretCache, previous *string) (TrustOutcome, error) {
	current, err := s.hostKeys.Get(ctx, node.Name)
	switch {
	case errors.Is(err, inventory.ErrNoTrustedHostKey):
	case err != nil:
		return "", fmt.Errorf("failed to read the trusted host key of node %q: %w", node.Name, err)
	case current.Fingerprint == request.Fingerprint:
		return TrustOutcomeUnchanged, nil
	default:
		*previous = current.Fingerprint
	}
	if *previous != request.Replace {
		return "", replaceRefusal(node.Name, *previous, request)
	}

	key, err := s.readHostKey(ctx, nodes, node, request.Fingerprint, secrets)
	if err != nil {
		return "", err
	}
	if _, err := s.hostKeys.Trust(ctx, node.Name, key, by, *previous); err != nil {
		return "", fmt.Errorf("failed to trust the host key of node %q: %w", node.Name, err)
	}
	if *previous != "" {
		return TrustOutcomeReplaced, nil
	}
	return TrustOutcomeTrusted, nil
}

func (s *Service) readHostKey(ctx context.Context, nodes []inventory.ResolvedNode, node inventory.ResolvedNode, fingerprint string, secrets *secretCache) (ssh.PublicKey, error) {
	bastion, err := bastionOf(nodes, node)
	if err != nil {
		return nil, err
	}
	cfg, err := sshconn.NodeConfig(node, bastion, s.hostKeys)
	if err != nil {
		return nil, err
	}
	var values map[string]string
	if bastion != nil {
		if values, err = secrets.resolve(ctx, sshconn.NodeSecretRefs(*bastion, nil)); err != nil {
			return nil, err
		}
	}
	key, err := sshconn.FindHostKey(sshconn.WithSecretValues(ctx, values), cfg, fingerprint)
	if err != nil {
		return nil, fmt.Errorf("failed to read the host key of node %q: %w", node.Name, err)
	}
	return key, nil
}

// Accepted names a host key the user accepted for a hop of a node's
// connection during a test, by the fingerprint the user saw.
type Accepted struct {
	Node        string
	Fingerprint string
}

// Stage returns a copy of nodes in which each accepted key is pinned as its
// hop's host key, in memory only, so the node named name can be tested
// through hops whose keys the user accepted but nobody trusted. The hops are
// the node and its bastion, if any, and each takes at most one key. Each key
// is read from its hop without sending it a credential. A hop that presents
// no key with the accepted fingerprint, or whose trusted key is another,
// fails the stage: accepting a key never replaces a trusted one.
func (s *Service) Stage(ctx context.Context, nodes []inventory.ResolvedNode, name string, accepted []Accepted) ([]inventory.ResolvedNode, error) {
	node, ok := findNode(nodes, name)
	if !ok {
		return nil, fmt.Errorf("node %q is not in the project's inventories", name)
	}
	hops, err := acceptedHops(node, accepted)
	if err != nil {
		return nil, err
	}
	staged := slices.Clone(nodes)
	secrets := s.newSecretCache()
	for _, key := range hops {
		i := slices.IndexFunc(staged, func(n inventory.ResolvedNode) bool { return strings.EqualFold(n.Name, key.Node) })
		if i < 0 {
			return nil, fmt.Errorf("node %q is not in the project's inventories", key.Node)
		}
		trusted, err := s.trustedFingerprint(ctx, staged[i].Name)
		switch {
		case err != nil:
			return nil, err
		case trusted == key.Fingerprint:
			continue
		case trusted != "":
			return nil, fmt.Errorf("node %q has the trusted host key %s; replace it rather than accept another", staged[i].Name, trusted)
		}
		readCtx, cancel := context.WithTimeout(ctx, nodeTimeout)
		read, err := s.readHostKey(readCtx, staged, staged[i], key.Fingerprint, secrets)
		cancel()
		if err != nil {
			return nil, err
		}
		staged[i].HostKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(read)))
	}
	return staged, nil
}

// acceptedHops orders the keys accepted for node by hop. The node is
// reached through its bastion, so the bastion's key comes first.
func acceptedHops(node inventory.ResolvedNode, accepted []Accepted) ([]Accepted, error) {
	var bastion, own []Accepted
	for _, key := range accepted {
		switch {
		case node.Bastion != "" && strings.EqualFold(key.Node, node.Bastion):
			bastion = append(bastion, key)
		case strings.EqualFold(key.Node, node.Name):
			own = append(own, key)
		default:
			return nil, fmt.Errorf("node %q is neither node %q nor its bastion", key.Node, node.Name)
		}
	}
	for _, keys := range [][]Accepted{bastion, own} {
		if len(keys) > 1 {
			return nil, fmt.Errorf("node %q has more than one accepted key", keys[0].Node)
		}
	}
	return append(bastion, own...), nil
}

// TrustAccepted trusts each accepted key as Stage pinned it in staged, once
// the node it was staged for is stored, since storing a node forgets any key
// trusted for its name. A key that is already trusted is left as it is. by
// names the user who trusts the keys. It returns the keys it trusted, also
// when it fails part way.
func (s *Service) TrustAccepted(ctx context.Context, staged []inventory.ResolvedNode, accepted []Accepted, by string) ([]inventory.TrustedHostKey, error) {
	var trusted []inventory.TrustedHostKey
	for _, key := range accepted {
		node, ok := findNode(staged, key.Node)
		if !ok {
			return trusted, fmt.Errorf("node %q is not in the project's inventories", key.Node)
		}
		current, err := s.trustedFingerprint(ctx, node.Name)
		if err != nil {
			return trusted, err
		}
		if current == key.Fingerprint {
			continue
		}
		public, _, _, _, err := ssh.ParseAuthorizedKey([]byte(node.HostKey))
		if err != nil || ssh.FingerprintSHA256(public) != key.Fingerprint {
			return trusted, fmt.Errorf("node %q has no staged host key %s", node.Name, key.Fingerprint)
		}
		stored, err := s.hostKeys.Trust(ctx, node.Name, public, by, "")
		if err != nil {
			return trusted, fmt.Errorf("failed to trust the host key of node %q: %w", node.Name, err)
		}
		trusted = append(trusted, stored)
	}
	return trusted, nil
}

// trustedFingerprint returns the fingerprint of the key trusted for node, or
// empty when none is.
func (s *Service) trustedFingerprint(ctx context.Context, node string) (string, error) {
	current, err := s.hostKeys.Get(ctx, node)
	switch {
	case errors.Is(err, inventory.ErrNoTrustedHostKey):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("failed to read the trusted host key of node %q: %w", node, err)
	}
	return current.Fingerprint, nil
}

// replaceRefusal says why request replaces nothing on node, whose trusted key
// has fingerprint current, or none when current is empty.
func replaceRefusal(node, current string, request TrustRequest) error {
	switch {
	case request.Replace == "":
		return fmt.Errorf("node %q already has trusted host key %s; replacing it with %s must be asked for", node, current, request.Fingerprint)
	case current == "":
		return fmt.Errorf("node %q has no trusted host key, so %s cannot be replaced", node, request.Replace)
	default:
		return fmt.Errorf("node %q trusts host key %s, not %s, which was asked to be replaced", node, current, request.Replace)
	}
}

// forEach runs do for each item, at most maxConnections at once and each
// within nodeTimeout, and passes each result to emit as it comes. Once ctx
// ends, no more items start, and those items emit nothing.
func forEach[T, R any](ctx context.Context, items []T, emit func(R), do func(context.Context, T) R) {
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		slots = make(chan struct{}, maxConnections)
	)
	defer wg.Wait()
	for _, item := range items {
		// select picks at random when a slot is free too.
		if ctx.Err() != nil {
			return
		}
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		wg.Go(func() {
			defer func() { <-slots }()
			itemCtx, cancel := context.WithTimeout(ctx, nodeTimeout)
			defer cancel()
			result := do(itemCtx, item)
			mu.Lock()
			defer mu.Unlock()
			emit(result)
		})
	}
}

func findNode(nodes []inventory.ResolvedNode, name string) (inventory.ResolvedNode, bool) {
	i := slices.IndexFunc(nodes, func(n inventory.ResolvedNode) bool { return strings.EqualFold(n.Name, name) })
	if i < 0 {
		return inventory.ResolvedNode{}, false
	}
	return nodes[i], true
}

// bastionOf returns the node that serves as node's bastion, or nil.
func bastionOf(nodes []inventory.ResolvedNode, node inventory.ResolvedNode) (*inventory.ResolvedNode, error) {
	if node.Bastion == "" {
		return nil, nil
	}
	bastion, ok := findNode(nodes, node.Bastion)
	if !ok {
		return nil, fmt.Errorf("bastion %q of node %q is not in the project's inventories", node.Bastion, node.Name)
	}
	return &bastion, nil
}

// secretCache resolves each secret ref once for all the nodes of one test
// or trust, since nodes often share their inventory's credentials.
type secretCache struct {
	resolver SecretResolver
	mu       sync.Mutex
	values   map[string]string
}

func (s *Service) newSecretCache() *secretCache {
	return &secretCache{resolver: s.secrets, values: map[string]string{}}
}

// resolve returns the values of refs, keyed by ref.
func (c *secretCache) resolve(ctx context.Context, refs []secretref.Ref) (map[string]string, error) {
	values := make(map[string]string, len(refs))
	for _, ref := range refs {
		value, err := c.value(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve %s from secret ref %q: %w", ref.Name, ref.Ref, err)
		}
		values[ref.Ref] = value
	}
	return values, nil
}

func (c *secretCache) value(ctx context.Context, ref secretref.Ref) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if value, ok := c.values[ref.Ref]; ok {
		return value, nil
	}
	if c.resolver == nil {
		return "", errors.New("secret store is not configured")
	}
	value, err := c.resolver.ResolveReference(ctx, ref)
	if err != nil {
		return "", err
	}
	c.values[ref.Ref] = value
	return value, nil
}
