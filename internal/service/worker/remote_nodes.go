// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
)

// remoteNodes finds the inventory nodes of one run on a worker, and the host
// keys trusted for them, through the coordinator (RFC 012 §Reading the
// inventory from a run). Each lookup asks the coordinator. A node's
// connection checks the host key of the latest answer that named the node,
// and each run has its own remoteNodes, so no answer outlives its run.
type remoteNodes struct {
	client coordinator.NodeClient
	owner  serviceregistry.HostInfo
	run    coordinator.WorkerAttempt

	mu sync.Mutex
	// keys holds, by lowercased name, each node an answer named, with its
	// trusted host key, or nil when it has none.
	keys map[string]*inventory.TrustedHostKey
}

var (
	_ runtime.NodeSelector    = (*remoteNodes)(nil)
	_ runtime.TrustedHostKeys = (*remoteNodes)(nil)
)

// newRemoteNodes returns the nodes of a run, or nil for both when the
// coordinator client cannot resolve nodes.
func newRemoteNodes(client coordinator.Client, owner serviceregistry.HostInfo, run coordinator.WorkerAttempt) (runtime.NodeSelector, runtime.TrustedHostKeys) {
	nodeClient, ok := client.(coordinator.NodeClient)
	if !ok {
		return nil, nil
	}
	nodes := &remoteNodes{client: nodeClient, owner: owner, run: run, keys: make(map[string]*inventory.TrustedHostKey)}
	return nodes, nodes
}

func (n *remoteNodes) Select(ctx context.Context, selector string) ([]inventory.ResolvedNode, error) {
	resolved, err := n.resolve(ctx, selector, nil)
	if err != nil {
		return nil, err
	}
	return resolved.Nodes, nil
}

func (n *remoteNodes) Named(ctx context.Context, names []string) ([]inventory.ResolvedNode, error) {
	if len(names) == 0 {
		return nil, nil
	}
	resolved, err := n.resolve(ctx, "", names)
	if err != nil {
		return nil, err
	}
	// A bastion that is also named is in both lists.
	nodes := slices.Clone(resolved.Nodes)
	for _, bastion := range resolved.Bastions {
		if !slices.ContainsFunc(nodes, func(node inventory.ResolvedNode) bool { return strings.EqualFold(node.Name, bastion.Name) }) {
			nodes = append(nodes, bastion)
		}
	}
	slices.SortFunc(nodes, func(a, b inventory.ResolvedNode) int { return strings.Compare(a.Name, b.Name) })
	return nodes, nil
}

// Get returns the key trusted for node in the latest answer that named it,
// and asks the coordinator when no answer has.
func (n *remoteNodes) Get(ctx context.Context, node string) (inventory.TrustedHostKey, error) {
	key, answered := n.trustedKey(node)
	if !answered {
		if _, err := n.resolve(ctx, "", []string{node}); err != nil {
			return inventory.TrustedHostKey{}, err
		}
		key, _ = n.trustedKey(node)
	}
	if key == nil {
		return inventory.TrustedHostKey{}, inventory.ErrNoTrustedHostKey
	}
	return *key, nil
}

func (n *remoteNodes) trustedKey(node string) (*inventory.TrustedHostKey, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	key, answered := n.keys[strings.ToLower(node)]
	return key, answered
}

// resolve asks the coordinator, and keeps the host keys of the nodes the
// answer names.
func (n *remoteNodes) resolve(ctx context.Context, selector string, names []string) (coordinator.ResolvedNodes, error) {
	resolved, err := n.client.ResolveNodes(ctx, n.owner, selector, names, n.run)
	if err != nil {
		return coordinator.ResolvedNodes{}, err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, node := range slices.Concat(resolved.Nodes, resolved.Bastions) {
		n.keys[strings.ToLower(node.Name)] = nil
	}
	for _, key := range resolved.HostKeys {
		n.keys[strings.ToLower(key.Node)] = &key
	}
	return resolved, nil
}
