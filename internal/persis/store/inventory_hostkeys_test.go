// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
)

type keyedInventories struct {
	store    *store.InventoryStore
	hostKeys *store.HostKeyStore
	tokyo    inventory.Stored
}

// newKeyedInventories stores inventory "tokyo" with nodes web01 and web02,
// and trusts a key for each and for db01, a node removed before.
func newKeyedInventories(t *testing.T) keyedInventories {
	t.Helper()
	backend := testutil.NewMemoryBackend()
	hostKeys := newHostKeyStore(t, backend)
	s, err := store.NewInventoryStore(backend.Collection(persis.CollectionInventories), backend.Lock(persis.CollectionInventories), hostKeys)
	require.NoError(t, err)
	tokyo, err := s.Create(context.Background(), testInventory("tokyo", "web01", "web02"))
	require.NoError(t, err)
	k := keyedInventories{store: s, hostKeys: hostKeys, tokyo: tokyo}
	for _, node := range []string{"web01", "web02", "db01"} {
		_, err := hostKeys.Trust(context.Background(), node, newPublicKey(t), "alice", "")
		require.NoError(t, err)
	}
	return k
}

// trusted reports, for each node, whether a key is trusted for it.
func (k keyedInventories) trusted(t *testing.T, nodes ...string) []bool {
	t.Helper()
	got := make([]bool, len(nodes))
	for i, node := range nodes {
		_, err := k.hostKeys.Get(context.Background(), node)
		if err != nil {
			require.ErrorIs(t, err, inventory.ErrNoTrustedHostKey)
		}
		got[i] = err == nil
	}
	return got
}

// A replace forgets the names it removes or adds, so a renamed node is
// trusted again, and keeps the key of a node whose host changes.
func TestInventoryStore_ReplaceForgetsChangedNames(t *testing.T) {
	t.Parallel()
	k := newKeyedInventories(t)

	inv := k.tokyo.Inventory
	inv.Nodes = []inventory.Node{{Name: "db01", Host: "10.0.0.1"}, {Name: "web02", Host: "10.0.9.12"}}
	_, err := k.store.Replace(context.Background(), inv, k.tokyo.Version)
	require.NoError(t, err)

	assert.Equal(t, []bool{false, true, false}, k.trusted(t, "web01", "web02", "db01"))
}

func TestInventoryStore_CreateAndDeleteForgetNames(t *testing.T) {
	t.Parallel()
	k := newKeyedInventories(t)
	ctx := context.Background()

	_, err := k.store.Create(ctx, testInventory("osaka", "DB01"))
	require.NoError(t, err)
	assert.Equal(t, []bool{true, true, false}, k.trusted(t, "web01", "web02", "db01"), "an added node starts with no key")

	require.NoError(t, k.store.Delete(ctx, k.tokyo.ID, k.tokyo.Version))
	assert.Equal(t, []bool{false, false}, k.trusted(t, "web01", "web02"))
}

// An import forgets the names it drops; a dry run forgets nothing.
func TestInventoryStore_ImportForgetsDroppedNames(t *testing.T) {
	t.Parallel()
	k := newKeyedInventories(t)
	ctx := context.Background()
	next := []inventory.Inventory{testInventory("tokyo", "web02")}

	_, err := k.store.Import(ctx, next, inventory.ImportOptions{Replace: true, DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, []bool{true, true}, k.trusted(t, "web01", "web02"))

	_, err = k.store.Import(ctx, next, inventory.ImportOptions{Replace: true})
	require.NoError(t, err)
	assert.Equal(t, []bool{false, true}, k.trusted(t, "web01", "web02"))
}

// failingForget is a host key store whose Forget fails.
type failingForget struct{ *store.HostKeyStore }

var errForget = errors.New("disk full")

func (failingForget) Forget(context.Context, string) error { return errForget }

// A change whose key cannot be forgotten stores nothing, so it never leaves
// a key for the next host of a name. Every writer forgets before it writes.
func TestInventoryStore_ChangeStopsWhenForgetFails(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(context.Context, *store.InventoryStore, inventory.Stored) error
	}{
		{"create", func(ctx context.Context, s *store.InventoryStore, _ inventory.Stored) error {
			_, err := s.Create(ctx, testInventory("osaka", "db01"))
			return err
		}},
		{"replace", func(ctx context.Context, s *store.InventoryStore, tokyo inventory.Stored) error {
			inv := tokyo.Inventory
			inv.Nodes = inv.Nodes[:1]
			_, err := s.Replace(ctx, inv, tokyo.Version)
			return err
		}},
		{"delete", func(ctx context.Context, s *store.InventoryStore, tokyo inventory.Stored) error {
			return s.Delete(ctx, tokyo.ID, tokyo.Version)
		}},
		{"import", func(ctx context.Context, s *store.InventoryStore, _ inventory.Stored) error {
			_, err := s.Import(ctx, []inventory.Inventory{testInventory("tokyo", "web01")}, inventory.ImportOptions{Replace: true})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			backend := testutil.NewMemoryBackend()
			col, lock := backend.Collection(persis.CollectionInventories), backend.Lock(persis.CollectionInventories)
			// A store that keeps no keys sets up the inventory, so only the
			// change under test forgets.
			setup, err := store.NewInventoryStore(col, lock, nil)
			require.NoError(t, err)
			tokyo, err := setup.Create(ctx, testInventory("tokyo", "web01", "web02"))
			require.NoError(t, err)
			s, err := store.NewInventoryStore(col, lock, failingForget{newHostKeyStore(t, backend)})
			require.NoError(t, err)

			require.ErrorIs(t, tt.change(ctx, s, tokyo), errForget)
			list, err := s.List(ctx)
			require.NoError(t, err)
			assert.Equal(t, []inventory.Stored{tokyo}, list)
		})
	}
}

var _ inventory.HostKeyStore = failingForget{}
