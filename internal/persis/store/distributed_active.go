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
// of a [persis.Collection]. Record IDs intentionally match the legacy
// file-backed distributed store SHA-256 key.
type ActiveDistributedRunStore struct {
	col persis.Collection
}

// NewActiveDistributedRunStore creates an ActiveDistributedRunStore backed by col.
func NewActiveDistributedRunStore(col persis.Collection) *ActiveDistributedRunStore {
	return &ActiveDistributedRunStore{col: col}
}

func (s *ActiveDistributedRunStore) Upsert(ctx context.Context, record exec.ActiveDistributedRun) error {
	if record.AttemptKey == "" {
		return fmt.Errorf("attempt key is required")
	}

	return s.withActiveRunLock(ctx, record.AttemptKey, func() error {
		now := time.Now().UTC()
		record.UpdatedAt = now.UnixMilli()
		return s.putActiveRun(ctx, record, now)
	})
}

func (s *ActiveDistributedRunStore) Delete(ctx context.Context, attemptKey string) error {
	if attemptKey == "" {
		return nil
	}
	return s.withActiveRunLock(ctx, attemptKey, func() error {
		if err := s.col.Delete(ctx, distributedRecordKey(attemptKey)); err != nil && !errors.Is(err, persis.ErrNotFound) {
			return err
		}
		return nil
	})
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

func (s *ActiveDistributedRunStore) putActiveRun(ctx context.Context, record exec.ActiveDistributedRun, updatedAt time.Time) error {
	id := distributedRecordKey(record.AttemptKey)
	createdAt := updatedAt
	if existing, err := s.col.Get(ctx, id); err == nil && !existing.CreatedAt.IsZero() {
		createdAt = existing.CreatedAt
	} else if err != nil && !errors.Is(err, persis.ErrNotFound) {
		return err
	}
	data, enc, err := persis.Encode(record)
	if err != nil {
		return err
	}
	return s.col.Put(ctx, &persis.Record{
		ID:        id,
		Data:      data,
		Encoding:  enc,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	})
}

func (s *ActiveDistributedRunStore) withActiveRunLock(ctx context.Context, attemptKey string, fn func() error) error {
	return withDistributedCollectionLock(ctx, s.col, distributedActiveRunLockKey(attemptKey), fn)
}
