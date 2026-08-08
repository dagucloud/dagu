// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

var _ dispatch.ActiveDistributedRunStore = (*ActiveDistributedRunStore)(nil)

// ActiveDistributedRunStore implements [dispatch.ActiveDistributedRunStore] on top
// of a [persis.Collection]. Record IDs intentionally match the file-backed
// distributed store SHA-256 key.
type ActiveDistributedRunStore struct {
	col                      persis.Collection
	corruptRecordGracePeriod time.Duration
}

// NewActiveDistributedRunStore creates an ActiveDistributedRunStore backed by col.
func NewActiveDistributedRunStore(col persis.Collection, opts ...DistributedStoreOption) *ActiveDistributedRunStore {
	resolved := resolveDistributedStoreOptions(opts)
	return &ActiveDistributedRunStore{
		col:                      col,
		corruptRecordGracePeriod: resolved.corruptRecordGracePeriod,
	}
}

// Upsert writes the active-run record. Get → Create if absent /
// CompareAndSwap if present, retrying on conflict.
func (s *ActiveDistributedRunStore) Upsert(ctx context.Context, record dispatch.ActiveDistributedRun) error {
	if record.AttemptKey == "" {
		return fmt.Errorf("attempt key is required")
	}
	id := distributedRecordKey(record.AttemptKey)

	return retryCAS(ctx, func(ctx context.Context) error {
		now := time.Now().UTC()
		record.UpdatedAt = now.UnixMilli()

		data, err := persis.Encode(record)
		if err != nil {
			return err
		}
		stored := &persis.Record{
			ID:        id,
			Data:      data,
			CreatedAt: now,
			UpdatedAt: now,
		}

		existing, getErr := s.col.Get(ctx, id)
		if errors.Is(getErr, persis.ErrCorrupt) {
			removed, retryErr := removeCorruptRecordForRetry(ctx, s.col, id, getErr)
			if removed {
				logger.Warn(ctx, "Removed corrupt active distributed run entry before replacement", tag.Name(id))
			}
			return retryErr
		}
		if getErr != nil && !errors.Is(getErr, persis.ErrNotFound) {
			return getErr
		}

		if existing == nil {
			return s.col.Create(ctx, stored)
		}
		casErr := s.col.CompareAndSwap(ctx, id, existing.Data, data)
		if errors.Is(casErr, persis.ErrNotFound) {
			return persis.ErrConflict
		}
		return casErr
	})
}

func (s *ActiveDistributedRunStore) Delete(ctx context.Context, attemptKey string) error {
	if attemptKey == "" {
		return nil
	}
	if err := s.col.Delete(ctx, distributedRecordKey(attemptKey)); err != nil && !errors.Is(err, persis.ErrNotFound) {
		return err
	}
	return nil
}

func (s *ActiveDistributedRunStore) Get(ctx context.Context, attemptKey string) (*dispatch.ActiveDistributedRun, error) {
	rec, err := s.col.Get(ctx, distributedRecordKey(attemptKey))
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, dispatch.ErrActiveRunNotFound
		}
		return nil, err
	}
	var record dispatch.ActiveDistributedRun
	if err := persis.Decode(rec, &record); err != nil {
		return nil, fmt.Errorf("active distributed run store: decode %q: %w", attemptKey, err)
	}
	return &record, nil
}

func (s *ActiveDistributedRunStore) ListAll(ctx context.Context) ([]dispatch.ActiveDistributedRun, error) {
	recs, err := listAllStrictWithReadError(ctx, s.col, persis.ListQuery{}, func(id string, readErr error) (bool, error) {
		if !errors.Is(readErr, persis.ErrCorrupt) {
			logSkippedActiveDistributedRun(ctx, id, readErr)
			return true, nil
		}

		removed, removeErr := removeStaleCorruptRecord(
			ctx,
			s.col,
			id,
			s.corruptRecordGracePeriod,
		)
		if errors.Is(removeErr, persis.ErrNotFound) {
			return true, nil
		}
		if removeErr != nil {
			logger.Warn(ctx, "Failed to remove corrupt active distributed run entry",
				tag.Name(id),
				tag.Error(removeErr),
			)
		} else if removed {
			logger.Warn(ctx, "Removed stale corrupt active distributed run entry",
				tag.Name(id),
				tag.Error(readErr),
			)
			return true, nil
		}
		logSkippedActiveDistributedRun(ctx, id, readErr)
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	records := make([]dispatch.ActiveDistributedRun, 0, len(recs))
	for _, rec := range recs {
		var record dispatch.ActiveDistributedRun
		if err := persis.Decode(rec, &record); err != nil {
			logSkippedActiveDistributedRun(ctx, rec.ID, err)
			continue
		}
		if record.AttemptKey == "" {
			continue
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].AttemptKey < records[j].AttemptKey
	})
	return records, nil
}

func logSkippedActiveDistributedRun(ctx context.Context, id string, err error) {
	logger.Warn(ctx, "Skipping corrupted active distributed run entry",
		tag.Name(id),
		tag.Error(err),
	)
}
