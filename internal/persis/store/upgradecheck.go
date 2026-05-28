// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/internal/persis"
	"github.com/dagucloud/dagu/internal/upgrade"
)

const upgradeCheckRecordID = "upgrade-check"

var _ upgrade.CacheStore = (*UpgradeCheckStore)(nil)

// UpgradeCheckStore implements [upgrade.CacheStore] over a single
// [persis.Collection] record.
type UpgradeCheckStore struct {
	col persis.Collection
}

// NewUpgradeCheckStore creates an UpgradeCheckStore backed by col.
func NewUpgradeCheckStore(col persis.Collection) *UpgradeCheckStore {
	return &UpgradeCheckStore{col: col}
}

// Load returns the cached upgrade-check data, or (nil, nil) when no usable
// record exists. Any read or decode failure is reported as a cache miss.
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
