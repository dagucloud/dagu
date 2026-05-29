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
	rec *SingleRecord[upgrade.UpgradeCheckCache]
}

// NewUpgradeCheckStore creates an UpgradeCheckStore backed by col.
func NewUpgradeCheckStore(col persis.Collection) *UpgradeCheckStore {
	return &UpgradeCheckStore{rec: NewSingleRecord[upgrade.UpgradeCheckCache](col, upgradeCheckRecordID)}
}

// Load returns the cached upgrade-check data, or (nil, nil) when no usable
// record exists. Any read or decode failure is reported as a cache miss.
func (s *UpgradeCheckStore) Load() (*upgrade.UpgradeCheckCache, error) {
	var cache upgrade.UpgradeCheckCache
	found, err := s.rec.Load(context.Background(), &cache)
	if err != nil || !found {
		return nil, nil
	}
	return &cache, nil
}

// Save replaces the cached upgrade-check data.
func (s *UpgradeCheckStore) Save(cache *upgrade.UpgradeCheckCache) error {
	if err := s.rec.Save(context.Background(), cache); err != nil {
		return fmt.Errorf("upgrade-check store: save: %w", err)
	}
	return nil
}
