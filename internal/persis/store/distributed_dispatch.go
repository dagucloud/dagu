// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

const (
	dispatchTaskStoreVersion      = 1
	defaultDispatchReservationTTL = exec.DefaultStaleLeaseThreshold

	dispatchPendingPrefix = "pending/"
	dispatchClaimsPrefix  = "claims/"
)

var _ exec.DispatchTaskStore = (*DispatchTaskStore)(nil)

// DispatchTaskStoreOption configures a DispatchTaskStore.
type DispatchTaskStoreOption func(*DispatchTaskStore)

// DispatchTaskStore implements [exec.DispatchTaskStore] on top of a
// [persis.Collection]. Record IDs use "pending/" and "claims/" prefixes so a
// file collection rooted at the legacy distributed directory reads the old
// on-disk layout without migration.
type DispatchTaskStore struct {
	col            persis.Collection
	reservationTTL time.Duration
	mu             sync.Mutex
}

type dispatchTaskPayload struct {
	Version      int                      `json:"version"`
	Task         *coordinatorv1.Task      `json:"task"`
	TaskFileName string                   `json:"taskFileName"`
	EnqueuedAt   int64                    `json:"enqueuedAt"`
	ClaimToken   string                   `json:"claimToken,omitempty"`
	ClaimedAt    int64                    `json:"claimedAt,omitempty"`
	WorkerID     string                   `json:"workerId,omitempty"`
	PollerID     string                   `json:"pollerId,omitempty"`
	Owner        exec.CoordinatorEndpoint `json:"owner,omitzero"`
}

// WithDispatchReservationTTL sets how long pending and claimed dispatch
// records can remain outstanding before cleanup recycles or removes them.
func WithDispatchReservationTTL(ttl time.Duration) DispatchTaskStoreOption {
	return func(store *DispatchTaskStore) {
		store.reservationTTL = normalizeDispatchReservationTTL(ttl)
	}
}

// NewDispatchTaskStore creates a DispatchTaskStore backed by col.
func NewDispatchTaskStore(col persis.Collection, opts ...DispatchTaskStoreOption) *DispatchTaskStore {
	s := &DispatchTaskStore{
		col:            col,
		reservationTTL: defaultDispatchReservationTTL,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *DispatchTaskStore) Enqueue(ctx context.Context, task *coordinatorv1.Task) error {
	if task == nil {
		return fmt.Errorf("task is required")
	}

	enqueuedAt := time.Now().UTC()
	fileName := fmt.Sprintf("task_%020d_%s.json", enqueuedAt.UnixMilli(), uuid.NewString())
	payload := dispatchTaskPayload{
		Version:      dispatchTaskStoreVersion,
		Task:         cloneDispatchTask(task),
		TaskFileName: fileName,
		EnqueuedAt:   enqueuedAt.UnixMilli(),
	}
	pendingID, err := pendingDispatchRecordID(fileName)
	if err != nil {
		return err
	}
	return s.putDispatchRecord(ctx, pendingID, payload, enqueuedAt, enqueuedAt)
}

func (s *DispatchTaskStore) ClaimNext(ctx context.Context, claim exec.DispatchTaskClaim) (*exec.ClaimedDispatchTask, error) {
	var claimed *exec.ClaimedDispatchTask
	err := s.withDispatchLock(ctx, func() error {
		if err := s.recycleExpiredReservationsLocked(ctx); err != nil {
			return err
		}

		recs, err := s.listDispatchRecords(ctx, dispatchPendingPrefix)
		if err != nil {
			return err
		}
		for _, rec := range recs {
			payload, err := dispatchTaskPayloadFromRecord(rec)
			if err != nil {
				return err
			}
			if payload.Task == nil || !matchesDispatchSelector(claim.Labels, payload.Task.WorkerSelector) {
				continue
			}

			claimToken := uuid.NewString()
			claimedAt := time.Now().UTC()
			task, err := applyDispatchTaskClaim(payload.Task, claim.Owner, claimToken)
			if err != nil {
				return err
			}
			payload.Task = task
			payload.ClaimToken = claimToken
			payload.ClaimedAt = claimedAt.UnixMilli()
			payload.WorkerID = claim.WorkerID
			payload.PollerID = claim.PollerID
			payload.Owner = claim.Owner

			claimRec, err := s.newDispatchRecord(claimDispatchRecordID(claimToken), payload, rec.CreatedAt, claimedAt)
			if err != nil {
				return err
			}
			if err := s.col.Put(ctx, claimRec); err != nil {
				return err
			}
			if err := s.col.CompareAndDelete(ctx, rec); err != nil {
				_ = s.col.CompareAndDelete(context.WithoutCancel(ctx), claimRec)
				if errors.Is(err, persis.ErrNotFound) || errors.Is(err, persis.ErrConflict) {
					continue
				}
				return err
			}

			claimed = &exec.ClaimedDispatchTask{
				Task:       cloneDispatchTask(task),
				ClaimToken: claimToken,
				ClaimedAt:  claimedAt,
				WorkerID:   claim.WorkerID,
				PollerID:   claim.PollerID,
				Owner:      claim.Owner,
			}
			return nil
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func (s *DispatchTaskStore) GetClaim(ctx context.Context, claimToken string) (*exec.ClaimedDispatchTask, error) {
	rec, err := s.col.Get(ctx, claimDispatchRecordID(claimToken))
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, exec.ErrDispatchTaskNotFound
		}
		return nil, err
	}
	payload, err := dispatchTaskPayloadFromRecord(rec)
	if err != nil {
		return nil, err
	}
	if payload.Task == nil || payload.ClaimToken == "" || payload.ClaimToken != claimToken || payload.ClaimedAt == 0 {
		return nil, exec.ErrDispatchTaskNotFound
	}
	return &exec.ClaimedDispatchTask{
		Task:       cloneDispatchTask(payload.Task),
		ClaimToken: payload.ClaimToken,
		ClaimedAt:  time.UnixMilli(payload.ClaimedAt).UTC(),
		WorkerID:   payload.WorkerID,
		PollerID:   payload.PollerID,
		Owner:      payload.Owner,
	}, nil
}

func (s *DispatchTaskStore) DeleteClaim(ctx context.Context, claimToken string) error {
	if err := s.col.Delete(ctx, claimDispatchRecordID(claimToken)); err != nil && !errors.Is(err, persis.ErrNotFound) {
		return err
	}
	return nil
}

func (s *DispatchTaskStore) CountOutstandingByQueue(ctx context.Context, queueName string, _ time.Duration) (int, error) {
	var count int
	err := s.withDispatchLock(ctx, func() error {
		if err := s.recycleExpiredReservationsLocked(ctx); err != nil {
			return err
		}
		payloads, err := s.outstandingDispatchPayloads(ctx)
		if err != nil {
			return err
		}
		for _, payload := range payloads {
			if payload.Task == nil {
				continue
			}
			if queueName != "" && payload.Task.QueueName != queueName {
				continue
			}
			count++
		}
		return nil
	})
	return count, err
}

func (s *DispatchTaskStore) HasOutstandingAttempt(ctx context.Context, attemptKey string, _ time.Duration) (bool, error) {
	if attemptKey == "" {
		return false, nil
	}

	var found bool
	err := s.withDispatchLock(ctx, func() error {
		if err := s.recycleExpiredReservationsLocked(ctx); err != nil {
			return err
		}
		payloads, err := s.outstandingDispatchPayloads(ctx)
		if err != nil {
			return err
		}
		for _, payload := range payloads {
			if payload.Task != nil && payload.Task.AttemptKey == attemptKey {
				found = true
				return nil
			}
		}
		return nil
	})
	return found, err
}

func (s *DispatchTaskStore) recycleExpiredReservationsLocked(ctx context.Context) error {
	if err := s.recycleExpiredClaimsLocked(ctx); err != nil {
		return err
	}
	if err := s.removePendingRecordsWithActiveClaimsLocked(ctx); err != nil {
		return err
	}
	return s.recycleExpiredPendingLocked(ctx)
}

func (s *DispatchTaskStore) recycleExpiredClaimsLocked(ctx context.Context) error {
	recs, err := s.listDispatchRecords(ctx, dispatchClaimsPrefix)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	for _, rec := range recs {
		payload, err := dispatchTaskPayloadFromRecord(rec)
		if err != nil {
			return err
		}
		claimedAt := dispatchRecordTimestamp(payload.ClaimedAt, rec.UpdatedAt)
		if now.Sub(claimedAt) < s.reservationTTL {
			continue
		}

		payload.EnqueuedAt = now.UnixMilli()
		payload.ClaimToken = ""
		payload.ClaimedAt = 0
		payload.WorkerID = ""
		payload.PollerID = ""
		payload.Owner = exec.CoordinatorEndpoint{}
		payload.Task = clearDispatchTaskClaim(payload.Task)

		pendingID, err := pendingDispatchRecordID(payload.TaskFileName)
		if err != nil {
			return err
		}
		pendingRec, err := s.newDispatchRecord(pendingID, payload, now, now)
		if err != nil {
			return err
		}
		if err := s.col.Put(ctx, pendingRec); err != nil {
			return err
		}
		if err := s.col.CompareAndDelete(ctx, rec); err != nil {
			if errors.Is(err, persis.ErrNotFound) || errors.Is(err, persis.ErrConflict) {
				continue
			}
			return err
		}
	}
	return nil
}

func (s *DispatchTaskStore) removePendingRecordsWithActiveClaimsLocked(ctx context.Context) error {
	claimRecs, err := s.listDispatchRecords(ctx, dispatchClaimsPrefix)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	activeTaskFiles := make(map[string]struct{}, len(claimRecs))
	for _, rec := range claimRecs {
		payload, err := dispatchTaskPayloadFromRecord(rec)
		if err != nil {
			return err
		}
		if payload.TaskFileName == "" || payload.ClaimToken == "" || payload.ClaimedAt == 0 {
			continue
		}
		claimedAt := dispatchRecordTimestamp(payload.ClaimedAt, rec.UpdatedAt)
		if now.Sub(claimedAt) >= s.reservationTTL {
			continue
		}
		activeTaskFiles[payload.TaskFileName] = struct{}{}
	}
	if len(activeTaskFiles) == 0 {
		return nil
	}

	pendingRecs, err := s.listDispatchRecords(ctx, dispatchPendingPrefix)
	if err != nil {
		return err
	}
	for _, rec := range pendingRecs {
		payload, err := dispatchTaskPayloadFromRecord(rec)
		if err != nil {
			return err
		}
		if _, ok := activeTaskFiles[payload.TaskFileName]; !ok {
			continue
		}
		if err := s.col.CompareAndDelete(ctx, rec); err != nil {
			if errors.Is(err, persis.ErrNotFound) || errors.Is(err, persis.ErrConflict) {
				continue
			}
			return err
		}
	}
	return nil
}

func (s *DispatchTaskStore) recycleExpiredPendingLocked(ctx context.Context) error {
	recs, err := s.listDispatchRecords(ctx, dispatchPendingPrefix)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	for _, rec := range recs {
		payload, err := dispatchTaskPayloadFromRecord(rec)
		if err != nil {
			return err
		}
		enqueuedAt := dispatchRecordTimestamp(payload.EnqueuedAt, rec.CreatedAt)
		if now.Sub(enqueuedAt) < s.reservationTTL {
			continue
		}
		if err := s.col.CompareAndDelete(ctx, rec); err != nil {
			if errors.Is(err, persis.ErrNotFound) || errors.Is(err, persis.ErrConflict) {
				continue
			}
			return err
		}
	}
	return nil
}

func (s *DispatchTaskStore) outstandingDispatchPayloads(ctx context.Context) ([]dispatchTaskPayload, error) {
	recs, err := s.listOutstandingDispatchRecords(ctx)
	if err != nil {
		return nil, err
	}
	payloads := make([]dispatchTaskPayload, 0, len(recs))
	for _, rec := range recs {
		payload, err := dispatchTaskPayloadFromRecord(rec)
		if err != nil {
			return nil, err
		}
		payloads = append(payloads, payload)
	}
	return payloads, nil
}

func (s *DispatchTaskStore) listOutstandingDispatchRecords(ctx context.Context) ([]*persis.Record, error) {
	pending, err := s.listDispatchRecords(ctx, dispatchPendingPrefix)
	if err != nil {
		return nil, err
	}
	claims, err := s.listDispatchRecords(ctx, dispatchClaimsPrefix)
	if err != nil {
		return nil, err
	}
	return append(pending, claims...), nil
}

func (s *DispatchTaskStore) listDispatchRecords(ctx context.Context, prefix string) ([]*persis.Record, error) {
	recs, err := listAllStrict(ctx, s.col, persis.ListQuery{Prefix: prefix})
	if err != nil {
		return nil, err
	}
	sort.Slice(recs, func(i, j int) bool {
		return recs[i].ID < recs[j].ID
	})
	return recs, nil
}

func (s *DispatchTaskStore) putDispatchRecord(ctx context.Context, id string, payload dispatchTaskPayload, createdAt, updatedAt time.Time) error {
	rec, err := s.newDispatchRecord(id, payload, createdAt, updatedAt)
	if err != nil {
		return err
	}
	return s.col.Put(ctx, rec)
}

func (s *DispatchTaskStore) newDispatchRecord(id string, payload dispatchTaskPayload, createdAt, updatedAt time.Time) (*persis.Record, error) {
	data, enc, err := persis.Encode(payload)
	if err != nil {
		return nil, err
	}
	return &persis.Record{
		ID:        id,
		Data:      data,
		Encoding:  enc,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}, nil
}

func (s *DispatchTaskStore) withDispatchLock(ctx context.Context, fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return withDistributedCollectionLock(ctx, s.col, "locks/dispatch_tasks", fn)
}

func dispatchTaskPayloadFromRecord(rec *persis.Record) (dispatchTaskPayload, error) {
	var payload dispatchTaskPayload
	if err := persis.Decode(rec, &payload); err != nil {
		return dispatchTaskPayload{}, fmt.Errorf("dispatch task store: decode %q: %w", rec.ID, err)
	}
	return payload, nil
}

func pendingDispatchRecordID(fileName string) (string, error) {
	name := normalizeDispatchRecordName(fileName)
	if name == "" {
		return "", fmt.Errorf("dispatch task store: task file name is required")
	}
	return dispatchPendingPrefix + name, nil
}

func claimDispatchRecordID(claimToken string) string {
	return dispatchClaimsPrefix + "claim_" + distributedRecordKey(claimToken)
}

func normalizeDispatchRecordName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.TrimSuffix(name, ".json")
	if name == "." || name == string(filepath.Separator) {
		return ""
	}
	return name
}

func normalizeDispatchReservationTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return defaultDispatchReservationTTL
	}
	return ttl
}

func dispatchRecordTimestamp(unixMillis int64, fallback time.Time) time.Time {
	if unixMillis > 0 {
		return time.UnixMilli(unixMillis).UTC()
	}
	if !fallback.IsZero() {
		return fallback.UTC()
	}
	return time.Now().UTC()
}

func cloneDispatchTask(task *coordinatorv1.Task) *coordinatorv1.Task {
	if task == nil {
		return nil
	}
	cloned, ok := proto.Clone(task).(*coordinatorv1.Task)
	if !ok {
		return nil
	}
	return cloned
}

func applyDispatchTaskClaim(task *coordinatorv1.Task, owner exec.CoordinatorEndpoint, claimToken string) (*coordinatorv1.Task, error) {
	task = cloneDispatchTask(task)
	if task == nil {
		return nil, nil
	}
	if owner.Port < 0 || owner.Port > math.MaxInt32 {
		return nil, fmt.Errorf("owner coordinator port out of range: %d", owner.Port)
	}
	task.OwnerCoordinatorId = owner.ID
	task.OwnerCoordinatorHost = owner.Host
	task.OwnerCoordinatorPort = int32(owner.Port)
	task.ClaimToken = claimToken
	return task, nil
}

func clearDispatchTaskClaim(task *coordinatorv1.Task) *coordinatorv1.Task {
	task = cloneDispatchTask(task)
	if task == nil {
		return nil
	}
	task.OwnerCoordinatorId = ""
	task.OwnerCoordinatorHost = ""
	task.OwnerCoordinatorPort = 0
	task.ClaimToken = ""
	task.WorkerId = ""
	return task
}

func matchesDispatchSelector(workerLabels, selector map[string]string) bool {
	if len(selector) == 0 {
		return true
	}
	for key, value := range selector {
		if workerLabels[key] != value {
			return false
		}
	}
	return true
}
