// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"time"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis"
)

// DistributedStoreOption configures file-corruption recovery for distributed
// control-plane stores.
type DistributedStoreOption func(*distributedStoreOptions)

type distributedStoreOptions struct {
	corruptRecordGracePeriod time.Duration
}

// WithCorruptRecordGracePeriod sets how long a corrupt distributed record
// remains fail-closed before it can be quarantined as stale.
func WithCorruptRecordGracePeriod(period time.Duration) DistributedStoreOption {
	return func(opts *distributedStoreOptions) {
		opts.corruptRecordGracePeriod = max(period, 0)
	}
}

func resolveDistributedStoreOptions(opts []DistributedStoreOption) distributedStoreOptions {
	resolved := distributedStoreOptions{corruptRecordGracePeriod: exec.DefaultStaleLeaseThreshold}
	for _, opt := range opts {
		opt(&resolved)
	}
	return resolved
}

type corruptRecordCollection interface {
	RepairCorrupt(ctx context.Context, rec *persis.Record) error
	QuarantineCorrupt(ctx context.Context, id string, staleBefore time.Time) (bool, error)
}

func repairCorruptRecord(ctx context.Context, col persis.Collection, rec *persis.Record) (bool, error) {
	repairer, ok := col.(corruptRecordCollection)
	if !ok {
		return false, nil
	}
	err := repairer.RepairCorrupt(ctx, rec)
	if errors.Is(err, persis.ErrNotFound) || errors.Is(err, persis.ErrConflict) {
		return false, persis.ErrConflict
	}
	return err == nil, err
}

func quarantineStaleCorruptRecord(
	ctx context.Context,
	col persis.Collection,
	id string,
	gracePeriod time.Duration,
) (bool, error) {
	quarantiner, ok := col.(corruptRecordCollection)
	if !ok {
		return false, nil
	}
	staleBefore := time.Now().UTC().Add(-gracePeriod)
	return quarantiner.QuarantineCorrupt(ctx, id, staleBefore)
}
