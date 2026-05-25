// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis"
)

var _ exec.DAGRunLeaseStore = (*DAGRunLeaseStore)(nil)

// DAGRunLeaseStore implements [exec.DAGRunLeaseStore] on top of a
// [persis.Collection]. Record IDs intentionally use the same SHA-256 key as the
// old file-backed distributed store, so existing lease files remain readable.
type DAGRunLeaseStore struct {
	col persis.Collection
}

// NewDAGRunLeaseStore creates a DAGRunLeaseStore backed by col.
func NewDAGRunLeaseStore(col persis.Collection) *DAGRunLeaseStore {
	return &DAGRunLeaseStore{col: col}
}

func (s *DAGRunLeaseStore) Upsert(ctx context.Context, lease exec.DAGRunLease) error {
	if lease.AttemptKey == "" {
		return fmt.Errorf("attempt key is required")
	}

	return s.withLeaseLock(ctx, lease.AttemptKey, func() error {
		now := time.Now().UTC()
		if lease.ClaimedAt == 0 {
			lease.ClaimedAt = now.UnixMilli()
			if lease.LastHeartbeatAt == 0 {
				lease.LastHeartbeatAt = lease.ClaimedAt
			}
		}
		if lease.LastHeartbeatAt == 0 {
			lease.LastHeartbeatAt = now.UnixMilli()
		}
		return s.putLease(ctx, lease, now)
	})
}

func (s *DAGRunLeaseStore) Touch(ctx context.Context, attemptKey string, observedAt time.Time) error {
	return s.withLeaseLock(ctx, attemptKey, func() error {
		lease, err := s.Get(ctx, attemptKey)
		if err != nil {
			return err
		}
		lease.LastHeartbeatAt = observedAt.UTC().UnixMilli()
		return s.putLease(ctx, *lease, time.Now().UTC())
	})
}

func (s *DAGRunLeaseStore) Delete(ctx context.Context, attemptKey string) error {
	return s.withLeaseLock(ctx, attemptKey, func() error {
		if err := s.col.Delete(ctx, distributedRecordKey(attemptKey)); err != nil && !errors.Is(err, persis.ErrNotFound) {
			return err
		}
		return nil
	})
}

func (s *DAGRunLeaseStore) Get(ctx context.Context, attemptKey string) (*exec.DAGRunLease, error) {
	rec, err := s.col.Get(ctx, distributedRecordKey(attemptKey))
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, exec.ErrDAGRunLeaseNotFound
		}
		return nil, err
	}
	var lease exec.DAGRunLease
	if err := persis.Decode(rec, &lease); err != nil {
		return nil, fmt.Errorf("dag-run lease store: decode %q: %w", attemptKey, err)
	}
	return &lease, nil
}

func (s *DAGRunLeaseStore) ListByQueue(ctx context.Context, queueName string) ([]exec.DAGRunLease, error) {
	leases, err := s.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	filtered := make([]exec.DAGRunLease, 0, len(leases))
	for _, lease := range leases {
		if lease.QueueName == queueName {
			filtered = append(filtered, lease)
		}
	}
	return filtered, nil
}

func (s *DAGRunLeaseStore) ListAll(ctx context.Context) ([]exec.DAGRunLease, error) {
	recs, err := listAllStrict(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return nil, err
	}
	leases := make([]exec.DAGRunLease, 0, len(recs))
	for _, rec := range recs {
		var lease exec.DAGRunLease
		if err := persis.Decode(rec, &lease); err != nil {
			return nil, fmt.Errorf("dag-run lease store: decode %q: %w", rec.ID, err)
		}
		if lease.AttemptKey == "" {
			continue
		}
		leases = append(leases, lease)
	}
	sort.Slice(leases, func(i, j int) bool {
		return leases[i].AttemptKey < leases[j].AttemptKey
	})
	return leases, nil
}

func (s *DAGRunLeaseStore) putLease(ctx context.Context, lease exec.DAGRunLease, updatedAt time.Time) error {
	id := distributedRecordKey(lease.AttemptKey)
	createdAt := updatedAt
	if existing, err := s.col.Get(ctx, id); err == nil && !existing.CreatedAt.IsZero() {
		createdAt = existing.CreatedAt
	} else if err != nil && !errors.Is(err, persis.ErrNotFound) {
		return err
	}
	data, enc, err := persis.Encode(lease)
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

func (s *DAGRunLeaseStore) withLeaseLock(ctx context.Context, attemptKey string, fn func() error) error {
	return withDistributedCollectionLock(ctx, s.col, distributedLeaseLockKey(attemptKey), fn)
}
