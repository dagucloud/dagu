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
// [persis.Collection]. Record IDs use the file-backed distributed store
// SHA-256 key.
type DAGRunLeaseStore struct {
	col persis.Collection
}

// NewDAGRunLeaseStore creates a DAGRunLeaseStore backed by col.
func NewDAGRunLeaseStore(col persis.Collection) *DAGRunLeaseStore {
	return &DAGRunLeaseStore{col: col}
}

// Upsert writes lease for the given attempt key. Uses optimistic concurrency:
// Get → Create if absent / CompareAndSwap if present, retrying on conflict.
// Replaces the previous file-lock-based pessimistic serialization.
func (s *DAGRunLeaseStore) Upsert(ctx context.Context, lease exec.DAGRunLease) error {
	if lease.AttemptKey == "" {
		return fmt.Errorf("attempt key is required")
	}
	id := distributedRecordKey(lease.AttemptKey)

	return retryCAS(ctx, func(ctx context.Context) error {
		now := time.Now().UTC()
		// Apply caller defaults — preserves today's L38-46 semantics.
		current := lease
		if current.ClaimedAt == 0 {
			current.ClaimedAt = now.UnixMilli()
			if current.LastHeartbeatAt == 0 {
				current.LastHeartbeatAt = current.ClaimedAt
			}
		}
		if current.LastHeartbeatAt == 0 {
			current.LastHeartbeatAt = now.UnixMilli()
		}

		existing, getErr := s.col.Get(ctx, id)
		if getErr != nil && !errors.Is(getErr, persis.ErrNotFound) {
			return getErr
		}

		data, enc, err := persis.Encode(current)
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
			// Record was deleted between Get and CAS — retry as Create.
			return persis.ErrConflict
		}
		return casErr
	})
}

// Touch updates LastHeartbeatAt to observedAt. Returns ErrDAGRunLeaseNotFound
// when the lease has been deleted (either initially or concurrently during
// retry) — preserves today's caller-visible behavior.
func (s *DAGRunLeaseStore) Touch(ctx context.Context, attemptKey string, observedAt time.Time) error {
	id := distributedRecordKey(attemptKey)

	return retryCAS(ctx, func(ctx context.Context) error {
		existing, err := s.col.Get(ctx, id)
		if err != nil {
			if errors.Is(err, persis.ErrNotFound) {
				return exec.ErrDAGRunLeaseNotFound
			}
			return err
		}
		var lease exec.DAGRunLease
		if err := persis.Decode(existing, &lease); err != nil {
			return fmt.Errorf("dag-run lease store: decode %q: %w", attemptKey, err)
		}
		lease.LastHeartbeatAt = observedAt.UTC().UnixMilli()
		next, _, err := persis.Encode(lease)
		if err != nil {
			return err
		}
		casErr := s.col.CompareAndSwap(ctx, id, existing.Data, next)
		if errors.Is(casErr, persis.ErrNotFound) {
			return exec.ErrDAGRunLeaseNotFound
		}
		return casErr
	})
}

func (s *DAGRunLeaseStore) Delete(ctx context.Context, attemptKey string) error {
	if err := s.col.Delete(ctx, distributedRecordKey(attemptKey)); err != nil && !errors.Is(err, persis.ErrNotFound) {
		return err
	}
	return nil
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
