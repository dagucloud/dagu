// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis"
)

var _ exec.ActiveDistributedRunStore = (*ActiveDistributedRunStore)(nil)

// ActiveDistributedRunStore implements [exec.ActiveDistributedRunStore] on top
// of a [persis.Collection]. Record IDs intentionally match the file-backed
// distributed store SHA-256 key.
type ActiveDistributedRunStore struct {
	col persis.Collection
}

// NewActiveDistributedRunStore creates an ActiveDistributedRunStore backed by col.
func NewActiveDistributedRunStore(col persis.Collection) *ActiveDistributedRunStore {
	return &ActiveDistributedRunStore{col: col}
}

// Upsert writes the active-run record. Uses optimistic concurrency:
// Get → Create if absent / CompareAndSwap if present, retrying on conflict.
func (s *ActiveDistributedRunStore) Upsert(ctx context.Context, record exec.ActiveDistributedRun) error {
	if record.AttemptKey == "" {
		return fmt.Errorf("attempt key is required")
	}
	id := distributedRecordKey(record.AttemptKey)

	return retryCAS(ctx, func(ctx context.Context) error {
		now := time.Now().UTC()
		record.UpdatedAt = now.UnixMilli()

		existing, getErr := s.col.Get(ctx, id)
		if getErr != nil && !errors.Is(getErr, persis.ErrNotFound) {
			return getErr
		}

		data, enc, err := persis.Encode(record)
		if err != nil {
			return err
		}

		if existing == nil {
			return s.col.Create(ctx, &persis.Record{
				ID:        id,
				Data:      data,
				Encoding:  enc,
				CreatedAt: now,
				UpdatedAt: now,
			})
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

func (s *ActiveDistributedRunStore) Get(ctx context.Context, attemptKey string) (*exec.ActiveDistributedRun, error) {
	rec, err := s.col.Get(ctx, distributedRecordKey(attemptKey))
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, exec.ErrActiveRunNotFound
		}
		return nil, err
	}
	var record exec.ActiveDistributedRun
	if err := persis.Decode(rec, &record); err != nil {
		return nil, fmt.Errorf("active distributed run store: decode %q: %w", attemptKey, err)
	}
	return &record, nil
}

func (s *ActiveDistributedRunStore) ListAll(ctx context.Context) ([]exec.ActiveDistributedRun, error) {
	recs, err := listAllBestEffort(ctx, s.col, persis.ListQuery{}, func(id string, err error) {
		logger.Warn(ctx, "Skipping corrupted active distributed run entry",
			tag.Name(id),
			tag.Error(err),
		)
	})
	if err != nil {
		return nil, err
	}
	records := make([]exec.ActiveDistributedRun, 0, len(recs))
	for _, rec := range recs {
		var record exec.ActiveDistributedRun
		if err := persis.Decode(rec, &record); err != nil {
			logger.Warn(ctx, "Skipping corrupted active distributed run entry",
				tag.Name(rec.ID),
				tag.Error(err),
			)
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

