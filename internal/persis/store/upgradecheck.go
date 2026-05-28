// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/internal/persis"
	"github.com/dagucloud/dagu/internal/upgrade"
)

// upgradeCheckRecordID is the single record ID under which upgrade-check
// cache data is stored. With the file backend, this maps to
// "{collection_dir}/upgrade-check.json" — byte-identical to the
// pre-refactor [fileupgradecheck] on-disk layout.
const upgradeCheckRecordID = "upgrade-check"

var _ upgrade.CacheStore = (*UpgradeCheckStore)(nil)

// UpgradeCheckStore implements [upgrade.CacheStore] by persisting a single
// record (id "upgrade-check") in a [persis.Collection].
type UpgradeCheckStore struct {
	col persis.Collection
}

// NewUpgradeCheckStore creates an UpgradeCheckStore backed by col.
func NewUpgradeCheckStore(col persis.Collection) *UpgradeCheckStore {
	return &UpgradeCheckStore{col: col}
}

// Load returns the cached upgrade-check data, or (nil, nil) when no record
// exists, when the on-disk data is unreadable, or when it fails to decode.
// Callers in [internal/upgrade] treat any non-cache result as a cache miss
// and trigger a fresh remote check, so collapsing read errors into a miss
// is observationally equivalent to the pre-refactor behavior.
func (s *UpgradeCheckStore) Load() (*upgrade.UpgradeCheckCache, error) {
	rec, err := s.col.Get(context.Background(), upgradeCheckRecordID)
	if err != nil {
		return nil, nil
	}
	var cache upgrade.UpgradeCheckCache
	if err := persis.Decode(rec, &cache); err != nil {
		return nil, nil
	}
	return &cache, nil
}

// Save replaces the cached upgrade-check data.
func (s *UpgradeCheckStore) Save(cache *upgrade.UpgradeCheckCache) error {
	data, err := persis.Encode(cache)
	if err != nil {
		return fmt.Errorf("upgrade-check store: encode: %w", err)
	}
	if err := s.col.Put(context.Background(), &persis.Record{
		ID:   upgradeCheckRecordID,
		Data: data,
	}); err != nil {
		return fmt.Errorf("upgrade-check store: save: %w", err)
	}
	return nil
}
