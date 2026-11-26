// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
)

// A step on the Dagu host matches inventory nodes and worker nodes alike,
// in name order, without a worker whose ID an inventory node holds.
func TestProjectNodesSelect(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := testutil.NewMemoryBackend()
	hostKeys, err := store.NewHostKeyStore(backend.Collection(persis.CollectionHostKeys))
	require.NoError(t, err)
	inventories, err := store.NewInventoryStore(backend.Collection(persis.CollectionInventories), backend.Lock(persis.CollectionInventories), hostKeys)
	require.NoError(t, err)
	_, err = inventories.Create(ctx, inventory.Inventory{Name: "prod", Nodes: []inventory.Node{
		{Name: "web01", Host: "10.0.1.11", Labels: []string{"web"}},
		{Name: "db01", Host: "10.0.1.21", Labels: []string{"db"}},
	}})
	require.NoError(t, err)
	workers, err := store.NewWorkerNodeStore(backend.Collection(persis.CollectionWorkerNodes), nil)
	require.NoError(t, err)
	for _, w := range []inventory.WorkerNode{
		{Name: "gw01", Host: "gw.tokyo", Labels: ir.NewLabels([]string{"web"})},
		{Name: "WEB01", Host: "clash", Labels: ir.NewLabels([]string{"web"})},
	} {
		require.NoError(t, workers.Record(ctx, w))
	}

	matched, err := projectNodes{inventories: inventories, workers: workers}.Select(ctx, "web")
	require.NoError(t, err)
	require.Len(t, matched, 2)
	assert.Equal(t, "gw01", matched[0].Name)
	assert.Equal(t, inventory.NodeKindWorker, matched[0].Kind)
	assert.Equal(t, "gw.tokyo", matched[0].Host)
	assert.Equal(t, "web01", matched[1].Name)
	assert.Equal(t, inventory.NodeKindSSH, matched[1].Kind)
}
