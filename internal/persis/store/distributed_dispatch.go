// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"sort"
	"strconv"
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
	minDispatchCleanupInterval    = 100 * time.Millisecond
	maxDispatchCleanupInterval    = time.Second

	dispatchPendingPrefix = "pending/"
	dispatchClaimsPrefix  = "claims/"
)

var (
	dispatchIndexReconcileInterval = 30 * time.Second
)

const dispatchNoMatchCacheLimit = 1024

var _ exec.DispatchTaskStore = (*DispatchTaskStore)(nil)

// DispatchTaskStoreOption configures a DispatchTaskStore.
type DispatchTaskStoreOption func(*DispatchTaskStore)

// DispatchTaskStore implements [exec.DispatchTaskStore] on top of a
// [persis.Collection]. Record IDs use "pending/" and "claims/" prefixes so a
// file collection rooted at the distributed directory uses the existing
// on-disk layout directly.
type DispatchTaskStore struct {
	col                      persis.Collection
	reservationTTL           time.Duration
	lastReservationCleanupAt time.Time
	index                    *dispatchTaskIndex
	// mu serializes the in-process recycle+scan+claim sequence;
	// per-record CompareAndDelete provides cross-process safety.
	mu sync.Mutex
}

type dispatchTaskIndex struct {
	pending      map[string]dispatchTaskIndexEntry
	pendingIDs   []string
	claims       map[string]dispatchTaskIndexEntry
	noMatch      map[string]struct{}
	reconciledAt time.Time
}

type dispatchTaskIndexEntry struct {
	id             string
	taskFileName   string
	queueName      string
	attemptKey     string
	claimToken     string
	workerSelector map[string]string
	hasTask        bool
	enqueuedAt     int64
	claimedAt      int64
	createdAt      time.Time
	updatedAt      time.Time
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

type legacyDAGRunStatusProto struct {
	JSONData string `json:"json_data,omitempty"`
}

var legacyDispatchTaskJSONFields = map[string]string{
	"root_dag_run_name":             "RootDAGRunName",
	"root_dag_run_id":               "RootDAGRunID",
	"parent_dag_run_name":           "ParentDAGRunName",
	"parent_dag_run_id":             "ParentDAGRunID",
	"operation":                     "Operation",
	"dag_run_id":                    "DAGRunID",
	"target":                        "Target",
	"definition":                    "Definition",
	"worker_id":                     "WorkerID",
	"attempt_id":                    "AttemptID",
	"attempt_key":                   "AttemptKey",
	"step":                          "Step",
	"params":                        "Params",
	"queue_name":                    "QueueName",
	"base_config":                   "BaseConfig",
	"labels":                        "Labels",
	"schedule_time":                 "ScheduleTime",
	"source_file":                   "SourceFile",
	"worker_selector":               "WorkerSelector",
	"agent_snapshot":                "AgentSnapshot",
	"external_step_retry":           "ExternalStepRetry",
	"workspace_bundle_digest":       "WorkspaceBundleDigest",
	"workspace_bundle_size":         "WorkspaceBundleSize",
	"workspace_bundle_dag_path":     "WorkspaceBundleDAGPath",
	"workspace_bundle_original_ref": "WorkspaceBundleOriginalRef",
	"workspace_bundle_resolved_ref": "WorkspaceBundleResolvedRef",
	"claim_token":                   "ClaimToken",
}

var legacyDispatchTaskJSONMarkers = func() [][]byte {
	markers := make([][]byte, 0, len(legacyDispatchTaskJSONFields)+4)
	for legacyKey := range legacyDispatchTaskJSONFields {
		markers = append(markers, []byte(`"`+legacyKey+`"`))
	}
	markers = append(markers,
		[]byte(`"previous_status"`),
		[]byte(`"owner_coordinator_id"`),
		[]byte(`"owner_coordinator_host"`),
		[]byte(`"owner_coordinator_port"`),
	)
	return markers
}()

func newDispatchTaskIndex() *dispatchTaskIndex {
	return &dispatchTaskIndex{
		pending: make(map[string]dispatchTaskIndexEntry),
		claims:  make(map[string]dispatchTaskIndexEntry),
		noMatch: make(map[string]struct{}),
	}
}

func (idx *dispatchTaskIndex) addPending(rec *persis.Record, payload dispatchTaskPayload) {
	if idx == nil || rec == nil {
		return
	}
	entry := dispatchTaskIndexEntryFromRecord(rec, payload)
	if _, ok := idx.pending[rec.ID]; !ok {
		idx.insertPendingID(rec.ID)
	}
	idx.pending[rec.ID] = entry
	idx.invalidateDerivedState()
}

func (idx *dispatchTaskIndex) removePending(id string) {
	if idx == nil {
		return
	}
	if _, ok := idx.pending[id]; !ok {
		return
	}
	delete(idx.pending, id)
	idx.removePendingID(id)
	idx.invalidateDerivedState()
}

func (idx *dispatchTaskIndex) addClaim(rec *persis.Record, payload dispatchTaskPayload) {
	if idx == nil || rec == nil {
		return
	}
	idx.claims[rec.ID] = dispatchTaskIndexEntryFromRecord(rec, payload)
	idx.invalidateDerivedState()
}

func (idx *dispatchTaskIndex) removeClaim(id string) {
	if idx == nil {
		return
	}
	if _, ok := idx.claims[id]; !ok {
		return
	}
	delete(idx.claims, id)
	idx.invalidateDerivedState()
}

func (idx *dispatchTaskIndex) replacePendingWithClaim(pendingID string, claimRec *persis.Record, payload dispatchTaskPayload) {
	if idx == nil {
		return
	}
	if _, ok := idx.pending[pendingID]; ok {
		delete(idx.pending, pendingID)
		idx.removePendingID(pendingID)
	}
	if claimRec != nil {
		idx.claims[claimRec.ID] = dispatchTaskIndexEntryFromRecord(claimRec, payload)
	}
	idx.invalidateDerivedState()
}

func (idx *dispatchTaskIndex) replaceClaimWithPending(claimID string, pendingRec *persis.Record, payload dispatchTaskPayload) {
	if idx == nil {
		return
	}
	delete(idx.claims, claimID)
	if pendingRec != nil {
		if _, ok := idx.pending[pendingRec.ID]; !ok {
			idx.insertPendingID(pendingRec.ID)
		}
		idx.pending[pendingRec.ID] = dispatchTaskIndexEntryFromRecord(pendingRec, payload)
	}
	idx.invalidateDerivedState()
}

func (idx *dispatchTaskIndex) insertPendingID(id string) {
	pos := sort.SearchStrings(idx.pendingIDs, id)
	if pos < len(idx.pendingIDs) && idx.pendingIDs[pos] == id {
		return
	}
	idx.pendingIDs = append(idx.pendingIDs, "")
	copy(idx.pendingIDs[pos+1:], idx.pendingIDs[pos:])
	idx.pendingIDs[pos] = id
}

func (idx *dispatchTaskIndex) removePendingID(id string) {
	pos := sort.SearchStrings(idx.pendingIDs, id)
	if pos >= len(idx.pendingIDs) || idx.pendingIDs[pos] != id {
		return
	}
	copy(idx.pendingIDs[pos:], idx.pendingIDs[pos+1:])
	idx.pendingIDs = idx.pendingIDs[:len(idx.pendingIDs)-1]
}

func (idx *dispatchTaskIndex) rebuildPendingIDs() {
	idx.pendingIDs = idx.pendingIDs[:0]
	for id := range idx.pending {
		idx.pendingIDs = append(idx.pendingIDs, id)
	}
	sort.Strings(idx.pendingIDs)
}

func (idx *dispatchTaskIndex) invalidateDerivedState() {
	clear(idx.noMatch)
}

func (idx *dispatchTaskIndex) candidatePendingIDs(workerLabels map[string]string) []string {
	ids := make([]string, 0, len(idx.pendingIDs))
	for _, id := range idx.pendingIDs {
		entry, ok := idx.pending[id]
		if !ok {
			continue
		}
		if matchesDispatchSelector(workerLabels, entry.workerSelector) {
			ids = append(ids, id)
		}
	}
	return ids
}

func (idx *dispatchTaskIndex) hasExpired(now time.Time, ttl time.Duration) bool {
	if idx == nil {
		return false
	}
	for _, entry := range idx.pending {
		enqueuedAt := dispatchRecordTimestamp(entry.enqueuedAt, entry.createdAt)
		if now.Sub(enqueuedAt) >= ttl {
			return true
		}
	}
	for _, entry := range idx.claims {
		claimedAt := dispatchRecordTimestamp(entry.claimedAt, entry.updatedAt)
		if now.Sub(claimedAt) >= ttl {
			return true
		}
	}
	return false
}

func (idx *dispatchTaskIndex) rememberNoMatch(labels map[string]string) {
	if idx == nil {
		return
	}
	key := dispatchClaimLabelsKey(labels)
	if _, ok := idx.noMatch[key]; !ok && len(idx.noMatch) >= dispatchNoMatchCacheLimit {
		clear(idx.noMatch)
	}
	idx.noMatch[key] = struct{}{}
}

func (idx *dispatchTaskIndex) hasNoMatch(labels map[string]string) bool {
	if idx == nil {
		return false
	}
	_, ok := idx.noMatch[dispatchClaimLabelsKey(labels)]
	return ok
}

func dispatchTaskIndexEntryFromRecord(rec *persis.Record, payload dispatchTaskPayload) dispatchTaskIndexEntry {
	entry := dispatchTaskIndexEntry{
		id:           rec.ID,
		taskFileName: payload.TaskFileName,
		claimToken:   payload.ClaimToken,
		enqueuedAt:   payload.EnqueuedAt,
		claimedAt:    payload.ClaimedAt,
		createdAt:    rec.CreatedAt,
		updatedAt:    rec.UpdatedAt,
	}
	if payload.Task != nil {
		entry.hasTask = true
		entry.queueName = payload.Task.QueueName
		entry.attemptKey = payload.Task.AttemptKey
		entry.workerSelector = maps.Clone(payload.Task.WorkerSelector)
	}
	return entry
}

func dispatchClaimLabelsKey(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		value := labels[key]
		b.WriteString(strconv.Itoa(len(key)))
		b.WriteByte(':')
		b.WriteString(key)
		b.WriteString(strconv.Itoa(len(value)))
		b.WriteByte(':')
		b.WriteString(value)
	}
	return b.String()
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

func (s *DispatchTaskStore) ensureDispatchIndex(ctx context.Context) error {
	if s.index == nil {
		return s.rebuildDispatchIndex(ctx)
	}
	return nil
}

func (s *DispatchTaskStore) rebuildDispatchIndex(ctx context.Context) error {
	idx, err := s.buildDispatchIndex(ctx)
	if err != nil {
		return err
	}
	idx.reconciledAt = time.Now().UTC()
	s.index = idx
	return nil
}

func (s *DispatchTaskStore) buildDispatchIndex(ctx context.Context) (*dispatchTaskIndex, error) {
	idx := newDispatchTaskIndex()
	pendingRecs, err := s.listDispatchRecords(ctx, dispatchPendingPrefix)
	if err != nil {
		return nil, err
	}
	for _, rec := range pendingRecs {
		payload, err := dispatchTaskPayloadFromRecord(rec)
		if err != nil {
			return nil, err
		}
		idx.pending[rec.ID] = dispatchTaskIndexEntryFromRecord(rec, payload)
	}
	idx.rebuildPendingIDs()

	claimRecs, err := s.listDispatchRecords(ctx, dispatchClaimsPrefix)
	if err != nil {
		return nil, err
	}
	for _, rec := range claimRecs {
		payload, err := dispatchTaskPayloadFromRecord(rec)
		if err != nil {
			return nil, err
		}
		idx.claims[rec.ID] = dispatchTaskIndexEntryFromRecord(rec, payload)
	}
	return idx, nil
}

func (s *DispatchTaskStore) reconcileDispatchIndexIDsIfDue(ctx context.Context, now time.Time) (bool, error) {
	if s.index == nil {
		if err := s.rebuildDispatchIndex(ctx); err != nil {
			return false, err
		}
		return true, nil
	}
	if !s.index.reconcileDue(now) {
		return false, nil
	}

	pendingIDs, err := s.listDispatchRecordIDs(ctx, dispatchPendingPrefix)
	if err != nil {
		return false, err
	}
	claimIDs, err := s.listDispatchRecordIDs(ctx, dispatchClaimsPrefix)
	if err != nil {
		return false, err
	}

	if !dispatchIndexIDsMatch(pendingIDs, s.index.pending) || !dispatchIndexIDsMatch(claimIDs, s.index.claims) {
		if err := s.rebuildDispatchIndex(ctx); err != nil {
			return false, err
		}
		return true, nil
	}

	s.index.reconciledAt = now
	return false, nil
}

func (idx *dispatchTaskIndex) reconcileDue(now time.Time) bool {
	if idx == nil || idx.reconciledAt.IsZero() {
		return true
	}
	if dispatchIndexReconcileInterval <= 0 {
		return true
	}
	return now.Sub(idx.reconciledAt) >= dispatchIndexReconcileInterval
}

func dispatchIndexIDsMatch(ids []string, indexed map[string]dispatchTaskIndexEntry) bool {
	if len(ids) != len(indexed) {
		return false
	}
	for _, id := range ids {
		if _, ok := indexed[id]; !ok {
			return false
		}
	}
	return true
}

func (s *DispatchTaskStore) Enqueue(ctx context.Context, task *exec.DispatchTask) error {
	if task == nil {
		return fmt.Errorf("task is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

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
	rec, err := s.newDispatchRecord(pendingID, payload, enqueuedAt, enqueuedAt)
	if err != nil {
		return err
	}
	if err := s.col.Put(ctx, rec); err != nil {
		return err
	}
	if s.index == nil {
		s.index = newDispatchTaskIndex()
	}
	s.index.addPending(rec, payload)
	return nil
}

// ClaimNext atomically transitions one matching pending record into a
// claim. CompareAndDelete(pending) is the per-task atomicity point;
// concurrent pollers racing on the same pending see one winner and the
// losers clean up their orphan claim and continue to the next pending.
func (s *DispatchTaskStore) ClaimNext(ctx context.Context, claim exec.DispatchTaskClaim) (*exec.ClaimedDispatchTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureDispatchIndex(ctx); err != nil {
		return nil, err
	}
	if _, err := s.maybeRecycleExpiredReservations(ctx); err != nil {
		return nil, err
	}

	for range 2 {
		claimed, stale, err := s.claimNextPending(ctx, claim)
		if err != nil || claimed != nil {
			return claimed, err
		}
		if stale {
			if err := s.rebuildDispatchIndex(ctx); err != nil {
				return nil, err
			}
			continue
		}
		rebuilt, err := s.reconcileDispatchIndexIDsIfDue(ctx, time.Now().UTC())
		if err != nil || !rebuilt {
			return nil, err
		}
	}
	return nil, nil
}

func (s *DispatchTaskStore) claimNextPending(ctx context.Context, claim exec.DispatchTaskClaim) (*exec.ClaimedDispatchTask, bool, error) {
	if s.index == nil {
		if err := s.rebuildDispatchIndex(ctx); err != nil {
			return nil, false, err
		}
	}
	if s.index.hasNoMatch(claim.Labels) {
		return nil, false, nil
	}
	ids := s.index.candidatePendingIDs(claim.Labels)
	if len(ids) == 0 {
		s.index.rememberNoMatch(claim.Labels)
		return nil, false, nil
	}

	now := time.Now().UTC()
	for _, id := range ids {
		rec, err := s.col.Get(ctx, id)
		if err != nil {
			if errors.Is(err, persis.ErrNotFound) {
				s.index.removePending(id)
				return nil, true, nil
			}
			return nil, false, fmt.Errorf("list record %q: %w", id, err)
		}
		payload, err := dispatchTaskPayloadFromRecord(rec)
		if err != nil {
			return nil, false, err
		}
		if payload.Task == nil {
			s.index.addPending(rec, payload)
			continue
		}
		enqueuedAt := dispatchRecordTimestamp(payload.EnqueuedAt, rec.CreatedAt)
		if now.Sub(enqueuedAt) >= s.reservationTTL {
			if err := s.col.CompareAndDelete(ctx, rec); err != nil {
				if errors.Is(err, persis.ErrNotFound) || errors.Is(err, persis.ErrConflict) {
					s.index.removePending(id)
					return nil, true, nil
				}
				return nil, false, err
			}
			s.index.removePending(id)
			continue
		}
		if !matchesDispatchSelector(claim.Labels, payload.Task.WorkerSelector) {
			s.index.addPending(rec, payload)
			continue
		}

		claimToken := uuid.NewString()
		claimedAt := now
		task, err := applyDispatchTaskClaim(payload.Task, claim.Owner, claimToken)
		if err != nil {
			return nil, false, err
		}
		payload.Task = task
		payload.ClaimToken = claimToken
		payload.ClaimedAt = claimedAt.UnixMilli()
		payload.WorkerID = claim.WorkerID
		payload.PollerID = claim.PollerID
		payload.Owner = claim.Owner

		claimRec, err := s.newDispatchRecord(claimDispatchRecordID(claimToken), payload, rec.CreatedAt, claimedAt)
		if err != nil {
			return nil, false, err
		}
		if err := s.col.Put(ctx, claimRec); err != nil {
			return nil, false, err
		}
		if err := s.col.CompareAndDelete(ctx, rec); err != nil {
			_ = s.col.CompareAndDelete(context.WithoutCancel(ctx), claimRec)
			if errors.Is(err, persis.ErrNotFound) || errors.Is(err, persis.ErrConflict) {
				s.index.removePending(id)
				return nil, true, nil
			}
			return nil, false, err
		}
		s.index.replacePendingWithClaim(id, claimRec, payload)

		return &exec.ClaimedDispatchTask{
			Task:       cloneDispatchTask(task),
			ClaimToken: claimToken,
			ClaimedAt:  claimedAt,
			WorkerID:   claim.WorkerID,
			PollerID:   claim.PollerID,
			Owner:      claim.Owner,
		}, false, nil
	}
	s.index.rememberNoMatch(claim.Labels)
	return nil, false, nil
}

func (s *DispatchTaskStore) maybeRecycleExpiredReservations(ctx context.Context) (bool, error) {
	now := time.Now().UTC()
	due := s.lastReservationCleanupAt.IsZero() ||
		now.Sub(s.lastReservationCleanupAt) >= dispatchCleanupInterval(s.reservationTTL)
	needed := s.index != nil && s.index.hasExpired(now, s.reservationTTL)
	if !due && !needed {
		return false, nil
	}
	if err := s.recycleExpiredReservations(ctx); err != nil {
		return false, err
	}
	s.lastReservationCleanupAt = now
	return true, nil
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
	s.mu.Lock()
	defer s.mu.Unlock()

	claimID := claimDispatchRecordID(claimToken)
	if err := s.col.Delete(ctx, claimID); err != nil && !errors.Is(err, persis.ErrNotFound) {
		return err
	}
	if s.index != nil {
		s.index.removeClaim(claimID)
	}
	return nil
}

// CountOutstandingByQueue returns the number of pending+claimed dispatch
// records matching queueName. External store changes can be invisible until the
// lazy ID reconciliation interval elapses. A task transitioning between pending
// and claim during the scan may be counted as both for a sub-millisecond window,
// which only under-reports available capacity.
func (s *DispatchTaskStore) CountOutstandingByQueue(ctx context.Context, queueName string, _ time.Duration) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureDispatchIndex(ctx); err != nil {
		return 0, err
	}
	if _, err := s.reconcileDispatchIndexIDsIfDue(ctx, time.Now().UTC()); err != nil {
		return 0, err
	}
	if err := s.recycleExpiredReservations(ctx); err != nil {
		return 0, err
	}
	var count int
	for _, entry := range s.index.pending {
		if !entry.hasTask {
			continue
		}
		if queueName != "" && entry.queueName != queueName {
			continue
		}
		count++
	}
	for _, entry := range s.index.claims {
		if !entry.hasTask {
			continue
		}
		if queueName != "" && entry.queueName != queueName {
			continue
		}
		count++
	}
	return count, nil
}

// HasOutstandingAttempt reports whether any pending or claimed record matches
// attemptKey. It uses the same bounded-staleness contract as
// [CountOutstandingByQueue].
func (s *DispatchTaskStore) HasOutstandingAttempt(ctx context.Context, attemptKey string, _ time.Duration) (bool, error) {
	if attemptKey == "" {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureDispatchIndex(ctx); err != nil {
		return false, err
	}
	if _, err := s.reconcileDispatchIndexIDsIfDue(ctx, time.Now().UTC()); err != nil {
		return false, err
	}
	if err := s.recycleExpiredReservations(ctx); err != nil {
		return false, err
	}
	for _, entry := range s.index.pending {
		if entry.attemptKey == attemptKey {
			return true, nil
		}
	}
	for _, entry := range s.index.claims {
		if entry.attemptKey == attemptKey {
			return true, nil
		}
	}
	return false, nil
}

func (s *DispatchTaskStore) recycleExpiredReservations(ctx context.Context) error {
	if s.index == nil {
		if err := s.rebuildDispatchIndex(ctx); err != nil {
			return err
		}
	}
	if err := s.recycleExpiredClaims(ctx); err != nil {
		return err
	}
	if err := s.removePendingRecordsWithActiveClaims(ctx); err != nil {
		return err
	}
	return s.recycleExpiredPending(ctx)
}

func (s *DispatchTaskStore) recycleExpiredClaims(ctx context.Context) error {
	if s.index == nil {
		return nil
	}
	now := time.Now().UTC()
	ids := make([]string, 0, len(s.index.claims))
	for id := range s.index.claims {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		entry, ok := s.index.claims[id]
		if !ok {
			continue
		}
		claimedAt := dispatchRecordTimestamp(entry.claimedAt, entry.updatedAt)
		if now.Sub(claimedAt) < s.reservationTTL {
			continue
		}

		rec, err := s.col.Get(ctx, id)
		if err != nil {
			if errors.Is(err, persis.ErrNotFound) {
				s.index.removeClaim(id)
				continue
			}
			return err
		}
		payload, err := dispatchTaskPayloadFromRecord(rec)
		if err != nil {
			return err
		}
		claimedAt = dispatchRecordTimestamp(payload.ClaimedAt, rec.UpdatedAt)
		if now.Sub(claimedAt) < s.reservationTTL {
			s.index.addClaim(rec, payload)
			continue
		}

		if err := s.releaseClaimRecord(ctx, rec, payload, now); err != nil {
			if errors.Is(err, persis.ErrNotFound) || errors.Is(err, persis.ErrConflict) {
				s.index.removeClaim(id)
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
	if err := s.col.CompareAndDelete(ctx, rec); err != nil {
		return err
	}
	if s.index != nil {
		s.index.replaceClaimWithPending(rec.ID, pendingRec, payload)
	}
	return nil
}

func (s *DispatchTaskStore) removePendingRecordsWithActiveClaims(ctx context.Context) error {
	if s.index == nil {
		return nil
	}
	now := time.Now().UTC()
	activeTaskFiles := make(map[string]time.Time, len(s.index.claims))
	for _, entry := range s.index.claims {
		if entry.taskFileName == "" || entry.claimToken == "" || entry.claimedAt == 0 {
			continue
		}
		claimedAt := dispatchRecordTimestamp(entry.claimedAt, entry.updatedAt)
		if now.Sub(claimedAt) < s.reservationTTL {
			if prev, ok := activeTaskFiles[entry.taskFileName]; !ok || claimedAt.After(prev) {
				activeTaskFiles[entry.taskFileName] = claimedAt
			}
		}
	}
	if len(activeTaskFiles) == 0 {
		return nil
	}
	ids := append([]string(nil), s.index.pendingIDs...)
	for _, id := range ids {
		entry, ok := s.index.pending[id]
		if !ok {
			continue
		}
		claimedAt, ok := activeTaskFiles[entry.taskFileName]
		if !ok {
			continue
		}
		enqueuedAt := dispatchRecordTimestamp(entry.enqueuedAt, entry.createdAt)
		if enqueuedAt.After(claimedAt) {
			continue
		}
		rec, err := s.col.Get(ctx, id)
		if err != nil {
			if errors.Is(err, persis.ErrNotFound) {
				s.index.removePending(id)
				continue
			}
			return err
		}
		payload, err := dispatchTaskPayloadFromRecord(rec)
		if err != nil {
			return err
		}
		claimedAt, ok = activeTaskFiles[payload.TaskFileName]
		if !ok {
			s.index.addPending(rec, payload)
			continue
		}
		enqueuedAt = dispatchRecordTimestamp(payload.EnqueuedAt, rec.CreatedAt)
		if enqueuedAt.After(claimedAt) {
			s.index.addPending(rec, payload)
			continue
		}
		if err := s.col.CompareAndDelete(ctx, rec); err != nil {
			if errors.Is(err, persis.ErrNotFound) || errors.Is(err, persis.ErrConflict) {
				s.index.removePending(id)
				continue
			}
			return err
		}
		s.index.removePending(id)
	}
	return nil
}

func (s *DispatchTaskStore) recycleExpiredPending(ctx context.Context) error {
	if s.index == nil {
		return nil
	}
	now := time.Now().UTC()
	ids := append([]string(nil), s.index.pendingIDs...)
	for _, id := range ids {
		entry, ok := s.index.pending[id]
		if !ok {
			continue
		}
		enqueuedAt := dispatchRecordTimestamp(entry.enqueuedAt, entry.createdAt)
		if now.Sub(enqueuedAt) < s.reservationTTL {
			continue
		}
		rec, err := s.col.Get(ctx, id)
		if err != nil {
			if errors.Is(err, persis.ErrNotFound) {
				s.index.removePending(id)
				continue
			}
			return err
		}
		payload, err := dispatchTaskPayloadFromRecord(rec)
		if err != nil {
			return err
		}
		enqueuedAt = dispatchRecordTimestamp(payload.EnqueuedAt, rec.CreatedAt)
		if now.Sub(enqueuedAt) < s.reservationTTL {
			s.index.addPending(rec, payload)
			continue
		}
		if err := s.col.CompareAndDelete(ctx, rec); err != nil {
			if errors.Is(err, persis.ErrNotFound) || errors.Is(err, persis.ErrConflict) {
				s.index.removePending(id)
				continue
			}
			return err
		}
		s.index.removePending(id)
	}
	return nil
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

func (s *DispatchTaskStore) listDispatchRecordIDs(ctx context.Context, prefix string) ([]string, error) {
	if idCol, ok := s.col.(strictRecordIDsCollection); ok {
		ids, err := idCol.RecordIDs(ctx, prefix)
		if err != nil {
			return nil, err
		}
		sort.Strings(ids)
		return ids, nil
	}

	recs, err := s.listDispatchRecords(ctx, prefix)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(recs))
	for _, rec := range recs {
		ids = append(ids, rec.ID)
	}
	return ids, nil
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
	if !hasLegacyDispatchTaskJSON(rec.Data) {
		return payload, nil
	}
	task, err := legacyDispatchTaskFromRecord(rec.Data)
	if err != nil {
		return dispatchTaskPayload{}, fmt.Errorf("dispatch task store: decode legacy task %q: %w", rec.ID, err)
	}
	if task != nil {
		payload.Task = task
	}
	return payload, nil
}

func hasLegacyDispatchTaskJSON(data []byte) bool {
	for _, marker := range legacyDispatchTaskJSONMarkers {
		if bytes.Contains(data, marker) {
			return true
		}
	}
	return false
}

func legacyDispatchTaskFromRecord(data []byte) (*exec.DispatchTask, error) {
	var raw struct {
		Task map[string]json.RawMessage `json:"task"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if len(raw.Task) == 0 {
		return nil, nil
	}

	current := make(map[string]json.RawMessage, len(raw.Task))
	var hasLegacyKeys bool
	for legacyKey, currentKey := range legacyDispatchTaskJSONFields {
		value, ok := raw.Task[legacyKey]
		if !ok {
			continue
		}
		hasLegacyKeys = true
		current[currentKey] = value
	}
	if statusData, ok := raw.Task["previous_status"]; ok {
		hasLegacyKeys = true
		previousStatus, err := legacyPreviousStatusJSON(statusData)
		if err != nil {
			return nil, err
		}
		if len(previousStatus) > 0 {
			current["PreviousStatus"] = previousStatus
		}
	}
	owner, ok, err := legacyOwnerJSON(raw.Task)
	if err != nil {
		return nil, err
	}
	if ok {
		hasLegacyKeys = true
		current["Owner"] = owner
	}
	if !hasLegacyKeys {
		return nil, nil
	}

	encoded, err := json.Marshal(current)
	if err != nil {
		return nil, err
	}
	var task exec.DispatchTask
	if err := json.Unmarshal(encoded, &task); err != nil {
		return nil, err
	}
	return &task, nil
}

func legacyPreviousStatusJSON(data json.RawMessage) (json.RawMessage, error) {
	var status legacyDAGRunStatusProto
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, fmt.Errorf("decode previous status wrapper: %w", err)
	}
	if status.JSONData == "" {
		return nil, nil
	}
	var decoded exec.DAGRunStatus
	if err := json.Unmarshal([]byte(status.JSONData), &decoded); err != nil {
		return nil, fmt.Errorf("decode previous status: %w", err)
	}
	return json.RawMessage(status.JSONData), nil
}

func legacyOwnerJSON(fields map[string]json.RawMessage) (json.RawMessage, bool, error) {
	var owner exec.CoordinatorEndpoint
	var ok bool
	if err := decodeLegacyOwnerField(fields, "owner_coordinator_id", &owner.ID, &ok); err != nil {
		return nil, false, err
	}
	if err := decodeLegacyOwnerField(fields, "owner_coordinator_host", &owner.Host, &ok); err != nil {
		return nil, false, err
	}
	if err := decodeLegacyOwnerField(fields, "owner_coordinator_port", &owner.Port, &ok); err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	data, err := json.Marshal(owner)
	return data, true, err
}

func decodeLegacyOwnerField[T any](fields map[string]json.RawMessage, key string, dst *T, ok *bool) error {
	value, exists := fields[key]
	if !exists {
		return nil
	}
	*ok = true
	return json.Unmarshal(value, dst)
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

func dispatchCleanupInterval(ttl time.Duration) time.Duration {
	interval := normalizeDispatchReservationTTL(ttl) / 10
	if interval < minDispatchCleanupInterval {
		return minDispatchCleanupInterval
	}
	if interval > maxDispatchCleanupInterval {
		return maxDispatchCleanupInterval
	}
	return interval
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
