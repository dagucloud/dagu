// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
)

// versionedCollection exposes the record IDs and versions of the memory
// collection it wraps, which a cached store compares, and counts the records
// read. A coarse one reports one version for every write, as a file system
// whose timestamps are coarser than the writes would.
type versionedCollection struct {
	persis.Collection
	gets   atomic.Int64
	coarse bool
}

func (c *versionedCollection) Get(ctx context.Context, id string) (*persis.Record, error) {
	c.gets.Add(1)
	return c.Collection.Get(ctx, id)
}

func (c *versionedCollection) RecordIDs(ctx context.Context, prefix string) ([]string, error) {
	return c.Collection.(*testutil.MemoryCollection).RecordIDs(ctx, prefix)
}

func (c *versionedCollection) RecordVersion(ctx context.Context, id string) (string, error) {
	version, err := c.Collection.(*testutil.MemoryCollection).RecordVersion(ctx, id)
	if c.coarse && err == nil {
		return "coarse", nil
	}
	return version, err
}

func newCachedInventoryStore(t *testing.T, col persis.Collection, lock persis.Lock) *store.InventoryStore {
	t.Helper()
	s, err := store.NewCachedInventoryStore(col, lock, nil)
	require.NoError(t, err)
	return s
}

// A cached store reads a change on the next read, whether it was made
// through the store or by another client of the collection.
func TestCachedInventoryStore_ReadsChanges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := testutil.NewMemoryBackend()
	col := &versionedCollection{Collection: backend.Collection(persis.CollectionInventories)}
	lock := backend.Lock(persis.CollectionInventories)
	cached := newCachedInventoryStore(t, col, lock)
	other, err := store.NewInventoryStore(col, lock, nil)
	require.NoError(t, err)

	tokyo, err := cached.Create(ctx, testInventory("Tokyo", "web01"))
	require.NoError(t, err)
	nodes, err := cached.Nodes(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"web01"}, nodeNames(nodes))

	osaka, err := other.Create(ctx, testInventory("Osaka", "osaka01"))
	require.NoError(t, err)
	nodes, err = cached.Nodes(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"osaka01", "web01"}, nodeNames(nodes))

	replaced := testInventory("Tokyo", "web01", "web02")
	replaced.ID = tokyo.ID
	_, err = other.Replace(ctx, replaced, tokyo.Version)
	require.NoError(t, err)
	selected, err := cached.Select(ctx, "web")
	require.NoError(t, err)
	assert.Equal(t, []string{"osaka01", "web01", "web02"}, nodeNames(selected))

	require.NoError(t, cached.Delete(ctx, osaka.ID, osaka.Version))
	nodes, err = cached.Nodes(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"web01", "web02"}, nodeNames(nodes))
}

// A change through the store is read on the next read even when the record
// versions do not show it.
func TestCachedInventoryStore_ReadsOwnChangeUnderCoarseVersions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := testutil.NewMemoryBackend()
	col := &versionedCollection{Collection: backend.Collection(persis.CollectionInventories), coarse: true}
	cached := newCachedInventoryStore(t, col, backend.Lock(persis.CollectionInventories))
	tokyo, err := cached.Create(ctx, testInventory("Tokyo", "web01"))
	require.NoError(t, err)
	_, err = cached.Nodes(ctx)
	require.NoError(t, err)

	replaced := testInventory("Tokyo", "web02")
	replaced.ID = tokyo.ID
	_, err = cached.Replace(ctx, replaced, tokyo.Version)
	require.NoError(t, err)
	nodes, err := cached.Nodes(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"web02"}, nodeNames(nodes))
}

// A read that finds no inventory changed reads no record, and a caller that
// changes the nodes it was given leaves the cache intact.
func TestCachedInventoryStore_ReadsNoRecordWhenUnchanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := testutil.NewMemoryBackend()
	col := &versionedCollection{Collection: backend.Collection(persis.CollectionInventories)}
	cached := newCachedInventoryStore(t, col, backend.Lock(persis.CollectionInventories))
	_, err := cached.Create(ctx, testInventory("Tokyo", "web01"))
	require.NoError(t, err)

	nodes, err := cached.Nodes(ctx)
	require.NoError(t, err)
	nodes[0].Labels[0].Key = "changed"

	reads := col.gets.Load()
	nodes, err = cached.Nodes(ctx)
	require.NoError(t, err)
	assert.Equal(t, reads, col.gets.Load())
	assert.Equal(t, "web", nodes[0].Labels[0].Key)
}

// A cached store whose nodes were read before an import that another client
// runs must not answer from them, or from the import part way, mid-import.
func TestCachedInventoryStore_ReadDuringImport(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := testutil.NewMemoryBackend()
	col := &swapHookCollection{Collection: &versionedCollection{Collection: backend.Collection(persis.CollectionInventories)}}
	lock := backend.Lock(persis.CollectionInventories)
	importer, err := store.NewInventoryStore(col, lock, nil)
	require.NoError(t, err)
	_, err = importer.Create(ctx, testInventory("Tokyo", "web01"))
	require.NoError(t, err)
	_, err = importer.Create(ctx, testInventory("Osaka"))
	require.NoError(t, err)
	cached := newCachedInventoryStore(t, col, lock)
	_, err = cached.Nodes(ctx)
	require.NoError(t, err)

	var (
		selected  []inventory.ResolvedNode
		selectErr error
		read      = make(chan struct{})
	)
	col.onSwap = func() {
		go func() {
			defer close(read)
			selected, selectErr = cached.Select(ctx, "web")
		}()
		// Give a read that skips the lock time to finish mid-import.
		select {
		case <-read:
		case <-time.After(200 * time.Millisecond):
		}
	}
	_, err = importer.Import(ctx, []inventory.Inventory{
		testInventory("Tokyo"),
		testInventory("Osaka", "web01"),
	}, inventory.ImportOptions{Replace: true})
	require.NoError(t, err)

	<-read
	require.NoError(t, selectErr)
	require.Len(t, selected, 1)
	assert.Equal(t, "Osaka", selected[0].InventoryName)
}
