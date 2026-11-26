// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package inventory_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// A worker is a node only when its ID names one; a worker without
// worker.id reports hostname@pid, and runs on the Dagu host report local.
func TestIsNodeName(t *testing.T) {
	t.Parallel()

	assert.True(t, inventory.IsNodeName("tokyo-gw"))
	assert.True(t, inventory.IsNodeName("Worker_1.prod"))
	assert.False(t, inventory.IsNodeName("build-host@4242"))
	assert.False(t, inventory.IsNodeName(""))
	assert.False(t, inventory.IsNodeName("-gw"))
	assert.False(t, inventory.IsNodeName("Local"), "the worker ID of runs on the Dagu host")
}

func TestSplitWorkers(t *testing.T) {
	t.Parallel()

	ssh := []inventory.ResolvedNode{{Name: "tokyo-web01", InventoryName: "Tokyo"}}
	workers := []inventory.WorkerNode{{Name: "osaka-gw"}, {Name: "Tokyo-Web01"}, {Name: "tokyo-gw"}}

	kept, conflicts := inventory.SplitWorkers(workers, ssh)

	assert.Equal(t, []inventory.WorkerNode{{Name: "osaka-gw"}, {Name: "tokyo-gw"}}, kept)
	assert.Equal(t, []inventory.WorkerConflict{{Worker: workers[1], Node: ssh[0]}}, conflicts)
}

// fakeInventories holds fixed inventory nodes.
type fakeInventories struct {
	inventory.Store
	nodes []inventory.ResolvedNode
}

func (f fakeInventories) Nodes(context.Context) ([]inventory.ResolvedNode, error) {
	return f.nodes, nil
}

// fakeWorkers holds fixed worker nodes and counts the reads of them.
type fakeWorkers struct {
	inventory.WorkerNodeStore
	workers []inventory.WorkerNode
	reads   *int
}

func (f fakeWorkers) List(context.Context) ([]inventory.WorkerNode, error) {
	if f.reads != nil {
		*f.reads++
	}
	return f.workers, nil
}

// Worker nodes join a project's nodes in name order and match alike, except
// a worker whose ID an inventory node holds.
func TestProjectNodes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	ssh := fakeInventories{nodes: []inventory.ResolvedNode{
		{Name: "tokyo-db01", Host: "10.0.1.21", Labels: ir.NewLabels([]string{"db"})},
		{Name: "tokyo-web01", Host: "10.0.1.11", Labels: ir.NewLabels([]string{"web"})},
	}}
	workers := fakeWorkers{workers: []inventory.WorkerNode{
		{Name: "osaka-gw", Host: "gw.osaka", Labels: ir.NewLabels([]string{"web"}), Online: true},
		{Name: "Tokyo-Web01", Host: "other", Labels: ir.NewLabels([]string{"web"})},
		{Name: "tokyo-gw", Host: "gw.tokyo"},
	}}
	nodes := inventory.NewProjectNodes(ssh, workers)

	all, err := nodes.Nodes(ctx)
	require.NoError(t, err)
	assert.Equal(t, []inventory.ResolvedNode{
		{Name: "osaka-gw", Kind: inventory.NodeKindWorker, Host: "gw.osaka", Labels: ir.NewLabels([]string{"web"})},
		ssh.nodes[0],
		{Name: "tokyo-gw", Kind: inventory.NodeKindWorker, Host: "gw.tokyo"},
		ssh.nodes[1],
	}, all)

	matched, err := nodes.Select(ctx, "web")
	require.NoError(t, err)
	assert.Equal(t, []inventory.ResolvedNode{all[0], all[3]}, matched)

	inventoryOnly, err := inventory.NewProjectNodes(ssh, nil).Nodes(ctx)
	require.NoError(t, err)
	assert.Equal(t, ssh.nodes, inventoryOnly)
}

// Named answers worker nodes by name too, and reads the worker nodes only
// when a name is no inventory node's.
func TestProjectNodesNamed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	ssh := fakeInventories{nodes: []inventory.ResolvedNode{
		{Name: "jump", Host: "10.0.1.1"},
		{Name: "web01", Host: "10.0.1.11", Bastion: "jump"},
	}}
	var reads int
	workers := fakeWorkers{workers: []inventory.WorkerNode{{Name: "tokyo-gw", Host: "gw.tokyo"}}, reads: &reads}
	nodes := inventory.NewProjectNodes(ssh, workers)

	named, err := nodes.Named(ctx, []string{"WEB01"})
	require.NoError(t, err)
	assert.Equal(t, ssh.nodes, named, "the node and its bastion")
	assert.Zero(t, reads, "every name is an inventory node's")

	named, err = nodes.Named(ctx, []string{"Tokyo-GW", "web01", "gone"})
	require.NoError(t, err)
	assert.Equal(t, []inventory.ResolvedNode{
		ssh.nodes[0],
		{Name: "tokyo-gw", Kind: inventory.NodeKindWorker, Host: "gw.tokyo"},
		ssh.nodes[1],
	}, named)
	assert.Equal(t, 1, reads)
}
