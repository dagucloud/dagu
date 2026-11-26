// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
)

// nodeRequest is one ResolveNodes call a run made.
type nodeRequest struct {
	selector string
	names    []string
	run      coordinator.SecretReferenceRun
}

// nodeCoordinator answers ResolveNodes from answer and records each call.
type nodeCoordinator struct {
	coordinator.Client
	answer   func(selector string, names []string) coordinator.ResolvedNodes
	requests []nodeRequest
}

func (c *nodeCoordinator) ResolveNodes(_ context.Context, _ serviceregistry.HostInfo, selector string, names []string, run coordinator.SecretReferenceRun) (coordinator.ResolvedNodes, error) {
	c.requests = append(c.requests, nodeRequest{selector: selector, names: names, run: run})
	return c.answer(selector, names), nil
}

var runIdentity = coordinator.SecretReferenceRun{WorkerID: "worker-1", AttemptKey: "key-1", AttemptID: "attempt-1", DAGName: "deploy"}

func newTestRemoteNodes(t *testing.T, client *nodeCoordinator) *remoteNodes {
	t.Helper()
	selector, keys := newRemoteNodes(client, serviceregistry.HostInfo{}, runIdentity)
	require.Same(t, selector, keys)
	return selector.(*remoteNodes)
}

func hostKey(node string) inventory.TrustedHostKey {
	return inventory.TrustedHostKey{Node: node, Key: "ssh-ed25519 " + node, Fingerprint: "SHA256:" + node}
}

// A lookup by name answers the named nodes and the bastions they connect
// through, each once, in name order, as the run identifies itself.
func TestRemoteNodesNamed(t *testing.T) {
	t.Parallel()

	client := &nodeCoordinator{answer: func(string, []string) coordinator.ResolvedNodes {
		return coordinator.ResolvedNodes{
			Nodes:    []inventory.ResolvedNode{{Name: "jump"}, {Name: "web-1", Bastion: "jump"}},
			Bastions: []inventory.ResolvedNode{{Name: "jump"}},
		}
	}}
	nodes := newTestRemoteNodes(t, client)

	named, err := nodes.Named(context.Background(), []string{"web-1", "jump"})
	require.NoError(t, err)
	assert.Equal(t, []inventory.ResolvedNode{{Name: "jump"}, {Name: "web-1", Bastion: "jump"}}, named)

	selected, err := nodes.Select(context.Background(), "role=web")
	require.NoError(t, err)
	assert.Len(t, selected, 2)
	assert.Equal(t, []nodeRequest{
		{names: []string{"web-1", "jump"}, run: runIdentity},
		{selector: "role=web", run: runIdentity},
	}, client.requests)
}

// A connection checks the host key of the latest answer that named its
// node. A node no answer named is asked for, and a node whose key an answer
// leaves out has none.
func TestRemoteNodesHostKeys(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	trusted := map[string]bool{"web-1": true, "db-1": true}
	client := &nodeCoordinator{answer: func(_ string, names []string) coordinator.ResolvedNodes {
		var resolved coordinator.ResolvedNodes
		for _, name := range names {
			resolved.Nodes = append(resolved.Nodes, inventory.ResolvedNode{Name: name})
			if trusted[strings.ToLower(name)] {
				resolved.HostKeys = append(resolved.HostKeys, hostKey(strings.ToLower(name)))
			}
		}
		return resolved
	}}
	nodes := newTestRemoteNodes(t, client)

	_, err := nodes.Named(ctx, []string{"web-1", "web-2"})
	require.NoError(t, err)
	key, err := nodes.Get(ctx, "WEB-1")
	require.NoError(t, err)
	assert.Equal(t, hostKey("web-1"), key)
	_, err = nodes.Get(ctx, "web-2")
	require.ErrorIs(t, err, inventory.ErrNoTrustedHostKey)
	require.Len(t, client.requests, 1, "the answer that named the nodes holds their keys")

	key, err = nodes.Get(ctx, "db-1")
	require.NoError(t, err)
	assert.Equal(t, hostKey("db-1"), key)
	require.Len(t, client.requests, 2)
	assert.Equal(t, []string{"db-1"}, client.requests[1].names)

	// A key no longer trusted is not served once an answer leaves it out.
	trusted["web-1"] = false
	_, err = nodes.Named(ctx, []string{"web-1"})
	require.NoError(t, err)
	_, err = nodes.Get(ctx, "web-1")
	require.ErrorIs(t, err, inventory.ErrNoTrustedHostKey)
}
