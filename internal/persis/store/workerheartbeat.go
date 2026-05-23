// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis"
)

var _ exec.WorkerHeartbeatStore = (*WorkerHeartbeatStore)(nil)

// WorkerHeartbeatStore implements [exec.WorkerHeartbeatStore].
// No secondary indices are needed; workerID is the primary key.
type WorkerHeartbeatStore struct {
	col persis.Collection
}

// NewWorkerHeartbeatStore creates a WorkerHeartbeatStore backed by col.
func NewWorkerHeartbeatStore(col persis.Collection) *WorkerHeartbeatStore {
	return &WorkerHeartbeatStore{col: col}
}

// Upsert inserts or overwrites the heartbeat record for a worker.
func (s *WorkerHeartbeatStore) Upsert(ctx context.Context, record exec.WorkerHeartbeatRecord) error {
	if record.WorkerID == "" {
		return fmt.Errorf("worker heartbeat store: workerID is required")
	}
	if record.LastHeartbeatAt == 0 {
		record.LastHeartbeatAt = time.Now().UTC().UnixMilli()
	}
	data, enc, err := persis.Encode(record)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return s.col.Put(ctx, &persis.Record{
		ID:        record.WorkerID,
		Data:      data,
		Encoding:  enc,
		CreatedAt: now,
		UpdatedAt: now,
	})
}

// Get retrieves the heartbeat record for a specific worker.
func (s *WorkerHeartbeatStore) Get(ctx context.Context, workerID string) (*exec.WorkerHeartbeatRecord, error) {
	if workerID == "" {
		return nil, exec.ErrWorkerHeartbeatNotFound
	}
	rec, err := s.col.Get(ctx, workerID)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, exec.ErrWorkerHeartbeatNotFound
		}
		return nil, err
	}
	var r exec.WorkerHeartbeatRecord
	if err := persis.Decode(rec, &r); err != nil {
		return nil, fmt.Errorf("worker heartbeat store: decode %q: %w", workerID, err)
	}
	if r.WorkerID == "" {
		return nil, exec.ErrWorkerHeartbeatNotFound
	}
	return &r, nil
}

// List returns all heartbeat records.
func (s *WorkerHeartbeatStore) List(ctx context.Context) ([]exec.WorkerHeartbeatRecord, error) {
	page, err := s.col.List(ctx, persis.ListQuery{})
	if err != nil {
		return nil, err
	}
	out := make([]exec.WorkerHeartbeatRecord, 0, len(page.Records))
	for _, rec := range page.Records {
		var r exec.WorkerHeartbeatRecord
		if err := persis.Decode(rec, &r); err != nil || r.WorkerID == "" {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// DeleteStale removes all records whose last heartbeat is before the given time.
// Returns the number of records deleted.
func (s *WorkerHeartbeatStore) DeleteStale(ctx context.Context, before time.Time) (int, error) {
	records, err := s.List(ctx)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, r := range records {
		if r.LastHeartbeatTime().After(before) {
			continue
		}
		if err := s.col.Delete(ctx, r.WorkerID); err != nil && !errors.Is(err, persis.ErrNotFound) {
			return removed, fmt.Errorf("worker heartbeat store: delete %q: %w", r.WorkerID, err)
		}
		removed++
	}
	return removed, nil
}
