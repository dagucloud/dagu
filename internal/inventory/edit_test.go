// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package inventory_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persiststore "github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
)

// newTokyo stores inventory "tokyo" with nodes web01 and web02.
func newTokyo(t *testing.T) (inventory.Store, inventory.Stored) {
	t.Helper()
	backend := testutil.NewMemoryBackend()
	store, err := persiststore.NewInventoryStore(backend.Collection(persis.CollectionInventories), backend.Lock(persis.CollectionInventories), nil)
	require.NoError(t, err)
	stored, err := store.Create(context.Background(), inventory.Inventory{
		Name:  "tokyo",
		Nodes: []inventory.Node{{Name: "web01", Host: "10.0.1.11"}, {Name: "web02", Host: "10.0.1.12"}},
	})
	require.NoError(t, err)
	return store, stored
}

// PutNode adds a node, or replaces the node of that name.
func TestPutNode(t *testing.T) {
	t.Parallel()
	store, tokyo := newTokyo(t)
	ctx := context.Background()

	stored, added, err := inventory.PutNode(ctx, store, tokyo.ID, inventory.Node{Name: "db01", Host: "10.0.2.11"}, tokyo.Version)
	require.NoError(t, err)
	assert.True(t, added)
	assert.Len(t, stored.Nodes, 3)

	stored, added, err = inventory.PutNode(ctx, store, tokyo.ID, inventory.Node{Name: "DB01", Host: "10.0.2.12"}, stored.Version)
	require.NoError(t, err)
	assert.False(t, added)
	require.Len(t, stored.Nodes, 3)
	assert.Equal(t, "10.0.2.12", stored.Nodes[2].Host)
}

func TestDeleteNode(t *testing.T) {
	t.Parallel()
	store, tokyo := newTokyo(t)

	stored, err := inventory.DeleteNode(context.Background(), store, tokyo.ID, "WEB01", tokyo.Version)
	require.NoError(t, err)
	require.Len(t, stored.Nodes, 1)
	assert.Equal(t, "web02", stored.Nodes[0].Name)
}

func TestNodeChangesRefuseStaleAndMissing(t *testing.T) {
	t.Parallel()
	store, tokyo := newTokyo(t)
	ctx := context.Background()

	_, _, err := inventory.PutNode(ctx, store, tokyo.ID, inventory.Node{Name: "db01", Host: "10.0.2.11"}, tokyo.Version+1)
	require.ErrorIs(t, err, inventory.ErrStale)
	_, err = inventory.DeleteNode(ctx, store, tokyo.ID, "web09", tokyo.Version)
	require.ErrorIs(t, err, inventory.ErrNodeNotFound)
	_, err = inventory.DeleteNode(ctx, store, "inv_missing", "web01", 1)
	require.ErrorIs(t, err, inventory.ErrNotFound)
}
