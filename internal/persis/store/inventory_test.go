// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
)

func newInventoryStore(t *testing.T, backend persis.Backend) *store.InventoryStore {
	t.Helper()
	s, err := store.NewInventoryStore(
		backend.Collection(persis.CollectionInventories),
		backend.Lock(persis.CollectionInventories),
	)
	require.NoError(t, err)
	return s
}

func newMemoryInventoryStore(t *testing.T) *store.InventoryStore {
	t.Helper()
	return newInventoryStore(t, testutil.NewMemoryBackend())
}

// testInventory returns an inventory with one node per name, each labeled
// "web".
func testInventory(name string, nodes ...string) inventory.Inventory {
	inv := inventory.Inventory{Name: name, Nodes: []inventory.Node{}}
	for _, n := range nodes {
		inv.Nodes = append(inv.Nodes, inventory.Node{Name: n, Host: n + ".example.com", Labels: []string{"web"}})
	}
	return inv
}

func nodeNames(nodes []inventory.ResolvedNode) []string {
	names := make([]string, len(nodes))
	for i, n := range nodes {
		names[i] = n.Name
	}
	return names
}

func storedNames(list []inventory.Stored) []string {
	names := make([]string, len(list))
	for i, inv := range list {
		names[i] = inv.Name
	}
	return names
}

func TestInventoryStore_Create(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newMemoryInventoryStore(t)

	tokyo, err := s.Create(ctx, testInventory("Tokyo", "tokyo-web01"))
	require.NoError(t, err)
	assert.Regexp(t, `^inv_[a-z2-7]{10}$`, tokyo.ID)
	assert.Equal(t, int64(1), tokyo.Version)
	assert.False(t, tokyo.CreatedAt.IsZero())

	_, err = s.Create(ctx, testInventory("osaka", "osaka-web01"))
	require.NoError(t, err)

	got, err := s.Get(ctx, tokyo.ID)
	require.NoError(t, err)
	assert.Equal(t, tokyo.Inventory, got.Inventory)
	assert.Equal(t, int64(1), got.Version)

	list, err := s.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"osaka", "Tokyo"}, storedNames(list), "names sort without case")

	nodes, err := s.Nodes(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"osaka-web01", "tokyo-web01"}, nodeNames(nodes))
	assert.Equal(t, tokyo.ID, nodes[1].InventoryID)
}

func TestInventoryStore_CreateRefusesBrokenRules(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newMemoryInventoryStore(t)
	_, err := s.Create(ctx, testInventory("Tokyo", "web01"))
	require.NoError(t, err)

	t.Run("NodeNameHeldByAnotherInventory", func(t *testing.T) {
		_, err := s.Create(ctx, testInventory("Osaka", "web01"))
		require.ErrorIs(t, err, inventory.ErrInvalid)
		assert.ErrorContains(t, err, `inventory "Osaka": node "web01": name: is also a node of inventory "Tokyo"`)
	})
	t.Run("NameTakenWithoutRegardToCase", func(t *testing.T) {
		_, err := s.Create(ctx, testInventory("TOKYO", "web02"))
		require.ErrorIs(t, err, inventory.ErrInvalid)
		assert.ErrorContains(t, err, `is also the name of inventory "Tokyo"`)
	})
	t.Run("IDGiven", func(t *testing.T) {
		inv := testInventory("Nagoya", "web03")
		inv.ID = "inv_aaaaaaaaaa"
		_, err := s.Create(ctx, inv)
		require.ErrorIs(t, err, inventory.ErrInvalid)
		assert.ErrorContains(t, err, "id: is set by Dagu")
	})

	list, err := s.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"Tokyo"}, storedNames(list))
}

func TestInventoryStore_Replace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newMemoryInventoryStore(t)
	created, err := s.Create(ctx, testInventory("Tokyo", "web01"))
	require.NoError(t, err)
	_, err = s.Create(ctx, testInventory("Osaka", "osaka01"))
	require.NoError(t, err)

	change := testInventory("Tokyo production", "web01", "web02")
	change.ID = created.ID
	replaced, err := s.Replace(ctx, change, created.Version)
	require.NoError(t, err)
	assert.Equal(t, created.ID, replaced.ID)
	assert.Equal(t, int64(2), replaced.Version)
	assert.Equal(t, created.CreatedAt, replaced.CreatedAt)

	got, err := s.Get(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "Tokyo production", got.Name)
	assert.Equal(t, int64(2), got.Version)
	assert.Len(t, got.Nodes, 2)

	t.Run("OlderVersion", func(t *testing.T) {
		_, err := s.Replace(ctx, change, created.Version)
		require.ErrorIs(t, err, inventory.ErrStale)
	})
	t.Run("BrokenRule", func(t *testing.T) {
		clash := testInventory("Tokyo production", "web01", "osaka01")
		clash.ID = created.ID
		_, err := s.Replace(ctx, clash, replaced.Version)
		assert.ErrorContains(t, err, `node "osaka01": name: is also a node of inventory "Osaka"`)
	})
	t.Run("UnknownID", func(t *testing.T) {
		missing := testInventory("Nagoya")
		missing.ID = "inv_aaaaaaaaaa"
		_, err := s.Replace(ctx, missing, 1)
		require.ErrorIs(t, err, inventory.ErrNotFound)
	})

	got, err = s.Get(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), got.Version, "refused changes store nothing")
}

func TestInventoryStore_Delete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newMemoryInventoryStore(t)
	created, err := s.Create(ctx, testInventory("Tokyo", "web01"))
	require.NoError(t, err)

	require.ErrorIs(t, s.Delete(ctx, created.ID, created.Version+1), inventory.ErrStale)
	require.NoError(t, s.Delete(ctx, created.ID, created.Version))

	_, err = s.Get(ctx, created.ID)
	require.ErrorIs(t, err, inventory.ErrNotFound)
	require.ErrorIs(t, s.Delete(ctx, created.ID, created.Version), inventory.ErrNotFound)
	// The node name is free again.
	_, err = s.Create(ctx, testInventory("Osaka", "web01"))
	require.NoError(t, err)
}

func TestInventoryStore_GetMalformedID(t *testing.T) {
	t.Parallel()
	s := newMemoryInventoryStore(t)
	for _, id := range []string{"", "../secrets/x", "inv_a/b", "INV_AAAA"} {
		_, err := s.Get(context.Background(), id)
		assert.ErrorIs(t, err, inventory.ErrNotFound, id)
	}
}

// Two stores on one data directory stand for the server and a CLI command.
// Each adds the same node name to a different inventory at once, and only one
// of them may succeed.
func TestInventoryStore_ConcurrentClashOneWins(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()
	stores := []*store.InventoryStore{
		newInventoryStore(t, file.NewBackend(config.PathsConfig{DataDir: dataDir})),
		newInventoryStore(t, file.NewBackend(config.PathsConfig{DataDir: dataDir})),
	}
	names := []string{"Tokyo", "Osaka"}

	var (
		wg    sync.WaitGroup
		start = make(chan struct{})
		errs  = make([]error, len(stores))
	)
	for i, s := range stores {
		wg.Go(func() {
			<-start
			_, errs[i] = s.Create(ctx, testInventory(names[i], "web01"))
		})
	}
	close(start)
	wg.Wait()

	list, err := stores[0].List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	winner := list[0].Name
	for i, err := range errs {
		if names[i] == winner {
			require.NoError(t, err)
			continue
		}
		require.ErrorIs(t, err, inventory.ErrInvalid)
		assert.ErrorContains(t, err, `is also a node of inventory "`+winner+`"`)
	}
}

func TestInventoryStore_Import(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	setup := func(t *testing.T) (*store.InventoryStore, inventory.Stored) {
		t.Helper()
		s := newMemoryInventoryStore(t)
		tokyo, err := s.Create(ctx, testInventory("Tokyo", "tokyo01"))
		require.NoError(t, err)
		return s, tokyo
	}

	t.Run("CreatesNewNames", func(t *testing.T) {
		t.Parallel()
		s, _ := setup(t)
		result, err := s.Import(ctx, []inventory.Inventory{
			testInventory("Osaka", "osaka01"),
			testInventory("Nagoya", "nagoya01"),
		}, inventory.ImportOptions{})
		require.NoError(t, err)
		assert.Equal(t, []string{"Osaka", "Nagoya"}, result.Created)
		assert.Empty(t, result.Replaced)

		list, err := s.List(ctx)
		require.NoError(t, err)
		assert.Equal(t, []string{"Nagoya", "Osaka", "Tokyo"}, storedNames(list))
	})

	t.Run("RefusesExistingNameAndListsEveryError", func(t *testing.T) {
		t.Parallel()
		s, _ := setup(t)
		_, err := s.Import(ctx, []inventory.Inventory{
			testInventory("Osaka", "osaka01"),
			testInventory("tokyo", "tokyo02"),
			testInventory("Nagoya", "tokyo01"),
		}, inventory.ImportOptions{})
		require.ErrorIs(t, err, inventory.ErrInvalid)
		assert.ErrorContains(t, err, `inventory "tokyo": name: already exists`)
		assert.ErrorContains(t, err, `inventory "Nagoya": node "tokyo01": name: is also a node of inventory "Tokyo"`)

		list, err := s.List(ctx)
		require.NoError(t, err)
		assert.Equal(t, []string{"Tokyo"}, storedNames(list), "a refused import stores nothing")
	})

	t.Run("ReplaceKeepsID", func(t *testing.T) {
		t.Parallel()
		s, tokyo := setup(t)
		result, err := s.Import(ctx, []inventory.Inventory{
			testInventory("TOKYO", "tokyo01", "tokyo02"),
		}, inventory.ImportOptions{Replace: true})
		require.NoError(t, err)
		assert.Equal(t, []string{"TOKYO"}, result.Replaced)

		got, err := s.Get(ctx, tokyo.ID)
		require.NoError(t, err)
		assert.Equal(t, "TOKYO", got.Name)
		assert.Equal(t, int64(2), got.Version)
		assert.Len(t, got.Nodes, 2)
	})

	t.Run("ReplaceMovesNodeBetweenInventories", func(t *testing.T) {
		t.Parallel()
		s, _ := setup(t)
		// tokyo01 leaves Tokyo and joins Osaka in one import.
		_, err := s.Import(ctx, []inventory.Inventory{
			testInventory("Tokyo"),
			testInventory("Osaka", "tokyo01"),
		}, inventory.ImportOptions{Replace: true})
		require.NoError(t, err)

		nodes, err := s.Nodes(ctx)
		require.NoError(t, err)
		require.Len(t, nodes, 1)
		assert.Equal(t, "Osaka", nodes[0].InventoryName)
	})

	t.Run("DryRunStoresNothing", func(t *testing.T) {
		t.Parallel()
		s, tokyo := setup(t)
		result, err := s.Import(ctx, []inventory.Inventory{
			testInventory("Tokyo", "tokyo01", "tokyo02"),
			testInventory("Osaka", "osaka01"),
		}, inventory.ImportOptions{Replace: true, DryRun: true})
		require.NoError(t, err)
		assert.Equal(t, []string{"Osaka"}, result.Created)
		assert.Equal(t, []string{"Tokyo"}, result.Replaced)

		list, err := s.List(ctx)
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, tokyo.Version, list[0].Version)
	})

	t.Run("RerunWithReplaceFinishesInterruptedImport", func(t *testing.T) {
		t.Parallel()
		s, _ := setup(t)
		batch := []inventory.Inventory{testInventory("Osaka", "osaka01"), testInventory("Nagoya", "nagoya01")}
		// An import that stopped after its first inventory.
		_, err := s.Import(ctx, batch[:1], inventory.ImportOptions{})
		require.NoError(t, err)

		result, err := s.Import(ctx, batch, inventory.ImportOptions{Replace: true})
		require.NoError(t, err)
		assert.Equal(t, []string{"Nagoya"}, result.Created)
		assert.Equal(t, []string{"Osaka"}, result.Replaced)

		list, err := s.List(ctx)
		require.NoError(t, err)
		assert.Equal(t, []string{"Nagoya", "Osaka", "Tokyo"}, storedNames(list))
	})

	t.Run("RefusesID", func(t *testing.T) {
		t.Parallel()
		s, tokyo := setup(t)
		inv := testInventory("Osaka", "osaka01")
		inv.ID = tokyo.ID
		_, err := s.Import(ctx, []inventory.Inventory{inv}, inventory.ImportOptions{Replace: true})
		assert.ErrorContains(t, err, `inventory "Osaka": id: is set by Dagu`)
	})
}

func TestInventoryStore_Select(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newMemoryInventoryStore(t)
	inv := testInventory("Tokyo", "web01", "web02")
	inv.Defaults.Labels = []string{"site=tokyo"}
	inv.Nodes = append(inv.Nodes, inventory.Node{Name: "db01", Host: "db01.example.com", Labels: []string{"db"}})
	_, err := s.Create(ctx, inv)
	require.NoError(t, err)

	nodes, err := s.Select(ctx, "site=tokyo,!db")
	require.NoError(t, err)
	assert.Equal(t, []string{"web01", "web02"}, nodeNames(nodes))

	_, err = s.Select(ctx, "")
	require.ErrorIs(t, err, inventory.ErrEmptySelector)
}

// staleReadCollection returns, once, the record a reader would have seen
// just before a change, as a read that races the change can.
type staleReadCollection struct {
	persis.Collection
	mu    sync.Mutex
	stale map[string]*persis.Record
}

func (c *staleReadCollection) Get(ctx context.Context, id string) (*persis.Record, error) {
	c.mu.Lock()
	rec, ok := c.stale[id]
	delete(c.stale, id)
	c.mu.Unlock()
	if ok {
		return rec, nil
	}
	return c.Collection.Get(ctx, id)
}

func (c *staleReadCollection) RecordIDs(ctx context.Context, prefix string) ([]string, error) {
	return c.Collection.(interface {
		RecordIDs(context.Context, string) ([]string, error)
	}).RecordIDs(ctx, prefix)
}

// A node moved between inventories appears in both to a read that sees one
// inventory before the move and the other after it. Such a read must not fail.
func TestInventoryStore_NodesDuringChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := testutil.NewMemoryBackend()
	col := &staleReadCollection{Collection: backend.Collection(persis.CollectionInventories)}
	s, err := store.NewInventoryStore(col, backend.Lock(persis.CollectionInventories))
	require.NoError(t, err)

	tokyo, err := s.Create(ctx, testInventory("Tokyo", "web01"))
	require.NoError(t, err)
	_, err = s.Create(ctx, testInventory("Osaka"))
	require.NoError(t, err)
	before, err := col.Collection.Get(ctx, tokyo.ID)
	require.NoError(t, err)
	_, err = s.Import(ctx, []inventory.Inventory{
		testInventory("Tokyo"),
		testInventory("Osaka", "web01"),
	}, inventory.ImportOptions{Replace: true})
	require.NoError(t, err)

	col.mu.Lock()
	col.stale = map[string]*persis.Record{tokyo.ID: before}
	col.mu.Unlock()

	nodes, err := s.Nodes(ctx)
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, "Osaka", nodes[0].InventoryName)
}
