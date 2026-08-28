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
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

var _ dispatch.DAGRunLeaseStore = (*DAGRunLeaseStore)(nil)

// DAGRunLeaseStore implements [dispatch.DAGRunLeaseStore] on top of a
// [persis.Collection]. Record IDs use the file-backed distributed store
// SHA-256 key.
type DAGRunLeaseStore struct {
	col                      persis.Collection
	corruptRecordGracePeriod time.Duration
}

// NewDAGRunLeaseStore creates a DAGRunLeaseStore backed by col.
func NewDAGRunLeaseStore(col persis.Collection, opts ...DistributedStoreOption) *DAGRunLeaseStore {
	resolved := resolveDistributedStoreOptions(opts)
	return &DAGRunLeaseStore{
		col:                      col,
		corruptRecordGracePeriod: resolved.corruptRecordGracePeriod,
	}
}

// Upsert creates or refreshes a lease without changing its claim identity.
func (s *DAGRunLeaseStore) Upsert(ctx context.Context, lease dispatch.DAGRunLease) error {
	if lease.AttemptKey == "" {
		return fmt.Errorf("attempt key is required")
	}
	id := distributedRecordKey(lease.AttemptKey)

	return retryConflict(ctx, func(ctx context.Context) error {
		now := time.Now().UTC()
		existing, getErr := s.col.Get(ctx, id)
		if errors.Is(getErr, persis.ErrCorrupt) {
			removed, retryErr := removeCorruptRecordForRetry(ctx, s.col, id, getErr)
			if removed {
				logger.Warn(ctx, "Removed corrupt distributed lease entry before replacement", tag.Name(id))
			}
			return retryErr
		}
		if getErr != nil && !errors.Is(getErr, persis.ErrNotFound) {
			return getErr
		}

		current := lease
		if existing != nil {
			var storedLease dispatch.DAGRunLease
			if err := persis.Decode(existing, &storedLease); err != nil {
				return fmt.Errorf("dag-run lease store: decode %q: %w", lease.AttemptKey, err)
			}
			var err error
			current, err = mergeDAGRunLease(storedLease, lease)
			if err != nil {
				return err
			}
		}
		if current.ClaimedAt == 0 {
			current.ClaimedAt = now.UnixMilli()
		}
		if current.LastHeartbeatAt == 0 {
			current.LastHeartbeatAt = current.ClaimedAt
		}
		data, err := persis.Encode(current)
		if err != nil {
			return err
		}
		createdAt := now
		if existing != nil {
			createdAt = existing.CreatedAt
		}
		return createOrSwap(ctx, s.col, existing, &persis.Record{
			ID: id, Data: data, CreatedAt: createdAt, UpdatedAt: now,
		})
	})
}

// Touch sets LastHeartbeatAt = observedAt. Returns ErrDAGRunLeaseNotFound
// when the lease is gone (initially or via concurrent delete).
func (s *DAGRunLeaseStore) Touch(ctx context.Context, attemptKey string, observedAt time.Time) error {
	id := distributedRecordKey(attemptKey)

	return retryConflict(ctx, func(ctx context.Context) error {
		existing, err := s.col.Get(ctx, id)
		if err != nil {
			if errors.Is(err, persis.ErrNotFound) {
				return dispatch.ErrDAGRunLeaseNotFound
			}
			return err
		}
		var lease dispatch.DAGRunLease
		if err := persis.Decode(existing, &lease); err != nil {
			return fmt.Errorf("dag-run lease store: decode %q: %w", attemptKey, err)
		}
		lease.LastHeartbeatAt = max(lease.LastHeartbeatAt, observedAt.UTC().UnixMilli())
		next, err := persis.Encode(lease)
		if err != nil {
			return err
		}
		return createOrSwap(ctx, s.col, existing, &persis.Record{
			ID: id, Data: next, CreatedAt: existing.CreatedAt, UpdatedAt: time.Now().UTC(),
		})
	})
}

func (s *DAGRunLeaseStore) Delete(ctx context.Context, attemptKey string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.col.Delete(ctx, distributedRecordKey(attemptKey))
}

func mergeDAGRunLease(current, incoming dispatch.DAGRunLease) (dispatch.DAGRunLease, error) {
	if current.AttemptKey != incoming.AttemptKey ||
		(current.DAGRun != (ir.DAGRunRef{}) && incoming.DAGRun != (ir.DAGRunRef{}) && current.DAGRun != incoming.DAGRun) ||
		(!current.Root.Zero() && !incoming.Root.Zero() && current.Root != incoming.Root) ||
		(current.AttemptID != "" && incoming.AttemptID != "" && current.AttemptID != incoming.AttemptID) ||
		(current.WorkerID != "" && incoming.WorkerID != "" && current.WorkerID != incoming.WorkerID) ||
		(current.ProfileName != "" && incoming.ProfileName != "" && current.ProfileName != incoming.ProfileName) ||
		(current.ClaimToken != "" && incoming.ClaimToken != "" && current.ClaimToken != incoming.ClaimToken) ||
		(current.WorkspaceBundleDigest != "" && incoming.WorkspaceBundleDigest != "" && current.WorkspaceBundleDigest != incoming.WorkspaceBundleDigest) {
		return dispatch.DAGRunLease{}, dispatch.ErrDAGRunLeaseConflict
	}
	if current.DAGRun == (ir.DAGRunRef{}) {
		current.DAGRun = incoming.DAGRun
	}
	if current.Root.Zero() {
		current.Root = incoming.Root
	}
	if current.AttemptID == "" {
		current.AttemptID = incoming.AttemptID
	}
	if current.QueueName == "" {
		current.QueueName = incoming.QueueName
	}
	if current.ProfileName == "" {
		current.ProfileName = incoming.ProfileName
	}
	if current.WorkerID == "" {
		current.WorkerID = incoming.WorkerID
	}
	if current.Owner.Host == "" {
		current.Owner.Host = incoming.Owner.Host
		current.Owner.Port = incoming.Owner.Port
	}
	if current.Owner.ID == "" {
		current.Owner.ID = incoming.Owner.ID
	}
	if current.ClaimToken == "" {
		current.ClaimToken = incoming.ClaimToken
	}
	if current.WorkspaceBundleDigest == "" {
		current.WorkspaceBundleDigest = incoming.WorkspaceBundleDigest
	}
	if current.ClaimedAt == 0 {
		current.ClaimedAt = incoming.ClaimedAt
	}
	current.LastHeartbeatAt = max(current.LastHeartbeatAt, incoming.LastHeartbeatAt)
	return current, nil
}

func (s *DAGRunLeaseStore) Get(ctx context.Context, attemptKey string) (*dispatch.DAGRunLease, error) {
	rec, err := s.col.Get(ctx, distributedRecordKey(attemptKey))
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, dispatch.ErrDAGRunLeaseNotFound
		}
		return nil, err
	}
	var lease dispatch.DAGRunLease
	if err := persis.Decode(rec, &lease); err != nil {
		return nil, fmt.Errorf("dag-run lease store: decode %q: %w", attemptKey, err)
	}
	return &lease, nil
}

func (s *DAGRunLeaseStore) ListByQueue(ctx context.Context, queueName string) ([]dispatch.DAGRunLease, error) {
	leases, err := s.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	filtered := make([]dispatch.DAGRunLease, 0, len(leases))
	for _, lease := range leases {
		if lease.QueueName == queueName {
			filtered = append(filtered, lease)
		}
	}
	return filtered, nil
}

func (s *DAGRunLeaseStore) ListAll(ctx context.Context) ([]dispatch.DAGRunLease, error) {
	recs, err := listAllStrictWithReadError(ctx, s.col, persis.ListQuery{}, func(id string, readErr error) (bool, error) {
		if !errors.Is(readErr, persis.ErrCorrupt) {
			return false, nil
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
			return false, removeErr
		}
		if !removed {
			return false, nil
		}
		logger.Warn(ctx, "Removed stale corrupt distributed lease entry",
			tag.Name(id),
			tag.Error(readErr),
		)
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	leases := make([]dispatch.DAGRunLease, 0, len(recs))
	for _, rec := range recs {
		var lease dispatch.DAGRunLease
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
