// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis"
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
// file collection rooted at the distributed directory uses the existing
// on-disk layout directly.
type DispatchTaskStore struct {
	col            persis.Collection
	reservationTTL time.Duration
	// mu serializes the in-process recycle+scan+claim sequence;
	// per-record CompareAndDelete provides cross-process safety.
	mu sync.Mutex
}

type dispatchTaskPayload struct {
	Version      int                      `json:"version"`
	Task         *exec.DispatchTask       `json:"task"`
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

func (s *DispatchTaskStore) Enqueue(ctx context.Context, task *exec.DispatchTask) error {
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

// ClaimNext atomically transitions one matching pending record into a
// claim. CompareAndDelete(pending) is the per-task atomicity point;
// concurrent pollers racing on the same pending see one winner and the
// losers clean up their orphan claim and continue to the next pending.
func (s *DispatchTaskStore) ClaimNext(ctx context.Context, claim exec.DispatchTaskClaim) (*exec.ClaimedDispatchTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.recycleExpiredReservations(ctx); err != nil {
		return nil, err
	}

	recs, err := s.listDispatchRecords(ctx, dispatchPendingPrefix)
	if err != nil {
		return nil, err
	}
	for _, rec := range recs {
		payload, err := dispatchTaskPayloadFromRecord(rec)
		if err != nil {
			return nil, err
		}
		if payload.Task == nil || !matchesDispatchSelector(claim.Labels, payload.Task.WorkerSelector) {
			continue
		}

		claimToken := uuid.NewString()
		claimedAt := time.Now().UTC()
		task, err := applyDispatchTaskClaim(payload.Task, claim.Owner, claimToken)
		if err != nil {
			return nil, err
		}
		payload.Task = task
		payload.ClaimToken = claimToken
		payload.ClaimedAt = claimedAt.UnixMilli()
		payload.WorkerID = claim.WorkerID
		payload.PollerID = claim.PollerID
		payload.Owner = claim.Owner

		claimRec, err := s.newDispatchRecord(claimDispatchRecordID(claimToken), payload, rec.CreatedAt, claimedAt)
		if err != nil {
			return nil, err
		}
		if err := s.col.Put(ctx, claimRec); err != nil {
			return nil, err
		}
		if err := s.col.CompareAndDelete(ctx, rec); err != nil {
			_ = s.col.CompareAndDelete(context.WithoutCancel(ctx), claimRec)
			if errors.Is(err, persis.ErrNotFound) || errors.Is(err, persis.ErrConflict) {
				continue
			}
			return nil, err
		}

		return &exec.ClaimedDispatchTask{
			Task:       cloneDispatchTask(task),
			ClaimToken: claimToken,
			ClaimedAt:  claimedAt,
			WorkerID:   claim.WorkerID,
			PollerID:   claim.PollerID,
			Owner:      claim.Owner,
		}, nil
	}
	return nil, nil
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

func (s *DispatchTaskStore) ReleaseClaim(ctx context.Context, claimToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.col.Get(ctx, claimDispatchRecordID(claimToken))
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return exec.ErrDispatchTaskNotFound
		}
		return err
	}
	payload, err := dispatchTaskPayloadFromRecord(rec)
	if err != nil {
		return err
	}
	if payload.Task == nil || payload.ClaimToken == "" || payload.ClaimToken != claimToken || payload.ClaimedAt == 0 {
		return exec.ErrDispatchTaskNotFound
	}
	return s.releaseClaimRecord(ctx, rec, payload, time.Now().UTC())
}

func (s *DispatchTaskStore) DeleteClaim(ctx context.Context, claimToken string) error {
	if err := s.col.Delete(ctx, claimDispatchRecordID(claimToken)); err != nil && !errors.Is(err, persis.ErrNotFound) {
		return err
	}
	return nil
}

// CountOutstandingByQueue returns the number of pending+claimed dispatch
// records matching queueName. A task transitioning between pending and
// claim during the scan may be counted as both for a sub-millisecond
// window — acceptable for observability.
func (s *DispatchTaskStore) CountOutstandingByQueue(ctx context.Context, queueName string, _ time.Duration) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.recycleExpiredReservations(ctx); err != nil {
		return 0, err
	}
	payloads, err := s.outstandingDispatchPayloads(ctx)
	if err != nil {
		return 0, err
	}
	var count int
	for _, payload := range payloads {
		if payload.Task == nil {
			continue
		}
		if queueName != "" && payload.Task.QueueName != queueName {
			continue
		}
		count++
	}
	return count, nil
}

// HasOutstandingAttempt reports whether any pending or claimed record
// matches attemptKey. Same eventual-consistency caveat as
// [CountOutstandingByQueue].
func (s *DispatchTaskStore) HasOutstandingAttempt(ctx context.Context, attemptKey string, _ time.Duration) (bool, error) {
	if attemptKey == "" {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.recycleExpiredReservations(ctx); err != nil {
		return false, err
	}
	payloads, err := s.outstandingDispatchPayloads(ctx)
	if err != nil {
		return false, err
	}
	for _, payload := range payloads {
		if payload.Task != nil && payload.Task.AttemptKey == attemptKey {
			return true, nil
		}
	}
	return false, nil
}

func (s *DispatchTaskStore) recycleExpiredReservations(ctx context.Context) error {
	if err := s.recycleExpiredClaims(ctx); err != nil {
		return err
	}
	if err := s.removePendingRecordsWithActiveClaims(ctx); err != nil {
		return err
	}
	return s.recycleExpiredPending(ctx)
}

func (s *DispatchTaskStore) recycleExpiredClaims(ctx context.Context) error {
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

		if err := s.releaseClaimRecord(ctx, rec, payload, now); err != nil {
			if errors.Is(err, persis.ErrNotFound) || errors.Is(err, persis.ErrConflict) {
				continue
			}
			return err
		}
	}
	return nil
}

func (s *DispatchTaskStore) releaseClaimRecord(ctx context.Context, rec *persis.Record, payload dispatchTaskPayload, now time.Time) error {
	releasedAt := now
	enqueuedAt := now.UnixMilli()
	if payload.ClaimedAt >= enqueuedAt {
		enqueuedAt = payload.ClaimedAt + 1
		releasedAt = time.UnixMilli(enqueuedAt).UTC()
	}
	payload.EnqueuedAt = enqueuedAt
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
	pendingRec, err := s.newDispatchRecord(pendingID, payload, releasedAt, releasedAt)
	if err != nil {
		return err
	}
	if err := s.col.Put(ctx, pendingRec); err != nil {
		return err
	}
	return s.col.CompareAndDelete(ctx, rec)
}

func (s *DispatchTaskStore) removePendingRecordsWithActiveClaims(ctx context.Context) error {
	claimRecs, err := s.listDispatchRecords(ctx, dispatchClaimsPrefix)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	activeTaskFiles := make(map[string]time.Time, len(claimRecs))
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
		if prev, ok := activeTaskFiles[payload.TaskFileName]; !ok || claimedAt.After(prev) {
			activeTaskFiles[payload.TaskFileName] = claimedAt
		}
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
		claimedAt, ok := activeTaskFiles[payload.TaskFileName]
		if !ok {
			continue
		}
		enqueuedAt := dispatchRecordTimestamp(payload.EnqueuedAt, rec.CreatedAt)
		if enqueuedAt.After(claimedAt) {
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

func (s *DispatchTaskStore) recycleExpiredPending(ctx context.Context) error {
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
	data, err := persis.Encode(payload)
	if err != nil {
		return nil, err
	}
	return &persis.Record{
		ID:        id,
		Data:      data,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}, nil
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

func cloneDispatchTask(task *exec.DispatchTask) *exec.DispatchTask {
	if task == nil {
		return nil
	}
	cloned := *task
	cloned.WorkerSelector = maps.Clone(task.WorkerSelector)
	cloned.AgentSnapshot = append([]byte(nil), task.AgentSnapshot...)
	return &cloned
}

func applyDispatchTaskClaim(task *exec.DispatchTask, owner exec.CoordinatorEndpoint, claimToken string) (*exec.DispatchTask, error) {
	task = cloneDispatchTask(task)
	if task == nil {
		return nil, nil
	}
	task.Owner = owner
	task.ClaimToken = claimToken
	return task, nil
}

func clearDispatchTaskClaim(task *exec.DispatchTask) *exec.DispatchTask {
	task = cloneDispatchTask(task)
	if task == nil {
		return nil
	}
	task.Owner = exec.CoordinatorEndpoint{}
	task.ClaimToken = ""
	task.WorkerID = ""
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
