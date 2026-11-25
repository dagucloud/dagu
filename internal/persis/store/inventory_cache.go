// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

// NewCachedInventoryStore creates an InventoryStore that keeps the project's
// resolved nodes in memory, for a long-running service. A read that finds no
// inventory changed since the last one lists the collection's records and
// their versions, and reads none of them. A change made through this store,
// or by any other client of the collection, is read again on the next read.
func NewCachedInventoryStore(col persis.Collection, lock persis.Lock, hostKeys inventory.HostKeyStore) (*InventoryStore, error) {
	s, err := NewInventoryStore(col, lock, hostKeys)
	if err != nil {
		return nil, err
	}
	s.cache = &inventoryCache{}
	return s, nil
}

// inventoryCache holds the resolved nodes read last, with the stamp of the
// records they were read from.
type inventoryCache struct {
	mu sync.Mutex
	// generation counts the changes made through the store, so a load that
	// raced with one is not kept.
	generation uint64
	snap       *inventorySnapshot
}

type inventorySnapshot struct {
	stamp      string
	generation uint64
	nodes      []inventory.ResolvedNode
	// err reports stored inventories that break a rule. It is kept, since
	// reading them again gives the same error until one changes.
	err error
}

// changed drops the cached nodes after a change through the store, whether
// or not it was stored in full.
func (c *inventoryCache) changed() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	c.snap = nil
}

// cachedNodes returns the resolved nodes, read again when an inventory
// changed. The stamp is compared without the lock. Every change is written
// under it, so a matching stamp means no record changed since the cached
// read, and one that differs leads to a read under the lock, which waits
// for a change in progress, such as an import, to finish.
func (s *InventoryStore) cachedNodes(ctx context.Context) ([]inventory.ResolvedNode, error) {
	stamp, stamped, err := s.stamp(ctx)
	if err != nil {
		return nil, err
	}
	s.cache.mu.Lock()
	snap, generation := s.cache.snap, s.cache.generation
	s.cache.mu.Unlock()

	if !stamped || snap == nil || snap.stamp != stamp {
		snap, err = s.loadSnapshot(ctx, generation)
		if err != nil {
			return nil, err
		}
		if stamped {
			s.cache.mu.Lock()
			if s.cache.generation == snap.generation {
				s.cache.snap = snap
			}
			s.cache.mu.Unlock()
		}
	}
	if snap.err != nil {
		return nil, snap.err
	}
	return cloneNodes(snap.nodes), nil
}

// loadSnapshot reads and resolves every inventory under the lock, with the
// stamp of the records it read.
func (s *InventoryStore) loadSnapshot(ctx context.Context, generation uint64) (*inventorySnapshot, error) {
	var snap *inventorySnapshot
	err := s.lock.Do(ctx, func(held func() error) error {
		stamp, _, err := s.stamp(ctx)
		if err != nil {
			return err
		}
		all, err := s.load(ctx)
		if err != nil {
			return err
		}
		if err := held(); err != nil {
			return err
		}
		nodes, err := inventory.Resolve(inventoriesOf(all))
		snap = &inventorySnapshot{stamp: stamp, generation: generation, nodes: nodes, err: err}
		return nil
	})
	return snap, err
}

// stamp returns a token that changes whenever an inventory is created,
// replaced, or deleted: each record's ID and version. stamped is false when
// the collection does not report record versions, so nothing is cached.
func (s *InventoryStore) stamp(ctx context.Context) (string, bool, error) {
	idCol, ok := s.col.(recordIDsCollection)
	if !ok {
		return "", false, nil
	}
	ids, err := idCol.RecordIDs(ctx, "")
	if err != nil {
		return "", false, err
	}
	var b strings.Builder
	for _, id := range ids {
		version, versioned, err := collectionRecordVersion(ctx, s.col, id)
		if !versioned {
			return "", false, nil
		}
		if errors.Is(err, persis.ErrNotFound) {
			// Deleted since it was listed.
			continue
		}
		if err != nil {
			return "", false, err
		}
		b.WriteString(id)
		b.WriteByte(0)
		b.WriteString(version)
		b.WriteByte('\n')
	}
	return b.String(), true, nil
}

// cloneNodes copies nodes and their labels, so a caller that changes them
// leaves the cache intact.
func cloneNodes(nodes []inventory.ResolvedNode) []inventory.ResolvedNode {
	out := slices.Clone(nodes)
	for i := range out {
		out[i].Labels = slices.Clone(out[i].Labels)
	}
	return out
}
