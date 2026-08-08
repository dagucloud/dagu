// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

// DistributedStoreOption configures file-corruption recovery for distributed
// control-plane stores.
type DistributedStoreOption func(*distributedStoreOptions)

type distributedStoreOptions struct {
	corruptRecordGracePeriod time.Duration
}

// WithCorruptRecordGracePeriod sets how long a corrupt distributed record
// remains fail-closed before it can be removed as stale.
func WithCorruptRecordGracePeriod(period time.Duration) DistributedStoreOption {
	return func(opts *distributedStoreOptions) {
		opts.corruptRecordGracePeriod = max(period, 0)
	}
}

func resolveDistributedStoreOptions(opts []DistributedStoreOption) distributedStoreOptions {
	resolved := distributedStoreOptions{corruptRecordGracePeriod: dagrun.DefaultStaleLeaseThreshold}
	for _, opt := range opts {
		opt(&resolved)
	}
	return resolved
}

type corruptRecordCollection interface {
	RemoveCorrupt(ctx context.Context, id string, staleBefore time.Time) (bool, error)
}

func removeCorruptRecord(
	ctx context.Context,
	col persis.Collection,
	id string,
	staleBefore time.Time,
) (bool, error) {
	remover, ok := col.(corruptRecordCollection)
	if !ok {
		return false, nil
	}
	return remover.RemoveCorrupt(ctx, id, staleBefore)
}

func removeCorruptRecordForRetry(
	ctx context.Context,
	col persis.Collection,
	id string,
	readErr error,
) (bool, error) {
	removed, err := removeCorruptRecord(ctx, col, id, time.Time{})
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) || errors.Is(err, persis.ErrConflict) {
			return false, persis.ErrConflict
		}
		return false, err
	}
	if !removed {
		return false, readErr
	}
	return true, persis.ErrConflict
}

func removeStaleCorruptRecord(
	ctx context.Context,
	col persis.Collection,
	id string,
	gracePeriod time.Duration,
) (bool, error) {
	staleBefore := time.Now().UTC().Add(-gracePeriod)
	return removeCorruptRecord(ctx, col, id, staleBefore)
}
