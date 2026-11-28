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
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

const (
	dispatchTaskStoreVersion      = 1
	defaultDispatchReservationTTL = dagrun.DefaultStaleLeaseThreshold
	minDispatchCleanupInterval    = 100 * time.Millisecond
	maxDispatchCleanupInterval    = time.Second

	dispatchPendingPrefix = "pending/"
	dispatchClaimsPrefix  = "claims/"
)

var (
	dispatchIndexReconcileInterval = 30 * time.Second
)

const dispatchNoMatchCacheLimit = 1024

var _ dispatch.DispatchTaskStore = (*DispatchTaskStore)(nil)
var _ dispatch.DispatchAdmissionStore = (*DispatchTaskStore)(nil)

// DispatchTaskStoreOption configures a DispatchTaskStore.
type DispatchTaskStoreOption func(*DispatchTaskStore)

// WithDispatchTransitionLock serializes task transitions with external users
// of the same shared lock.
func WithDispatchTransitionLock(
	lock func(context.Context, func(context.Context) error) error,
) DispatchTaskStoreOption {
	return func(store *DispatchTaskStore) {
		store.transitionLock = lock
	}
}

// DispatchTaskStore implements [dispatch.DispatchTaskStore] on top of a
// [persis.Collection]. Record IDs use "pending/" and "claims/" prefixes so a
// file collection rooted at the distributed directory uses the existing
// on-disk layout directly.
type DispatchTaskStore struct {
	col                      persis.Collection
	reservationTTL           time.Duration
	admissionLeaseStore      dispatch.DAGRunLeaseStore
	admissionActiveRunStore  dispatch.ActiveDistributedRunStore
	lastReservationCleanupAt time.Time
	index                    *dispatchTaskIndex
	transitionLock           func(context.Context, func(context.Context) error) error
	// mu protects the in-memory index and serializes dispatch transitions;
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
	targetWorkerID string
	workerSelector map[string]string
	hasTask        bool
	enqueuedAt     int64
	claimedAt      int64
	// holdUntil is the task's HoldUntil in Unix milliseconds, 0 when the
	// task was not held for a worker.
	holdUntil int64
	createdAt time.Time
	updatedAt time.Time

	// needsConnectionTokens is the task's NeedsConnectionTokens.
	needsConnectionTokens bool
}

type dispatchTaskPayload struct {
	Version                   int                          `json:"version"`
	Task                      *dispatch.DispatchTask       `json:"task"`
	TaskFileName              string                       `json:"taskFileName"`
	EnqueuedAt                int64                        `json:"enqueuedAt"`
	ClaimToken                string                       `json:"claimToken,omitempty"`
	ClaimedAt                 int64                        `json:"claimedAt,omitempty"`
	WorkerID                  string                       `json:"workerId,omitempty"`
	PollerID                  string                       `json:"pollerId,omitempty"`
	Owner                     dispatch.CoordinatorEndpoint `json:"owner,omitzero"`
	AdmissionReservationToken string                       `json:"admissionReservationToken,omitempty"`
	// LeasingAt is when an acceptance of the claim began writing the
	// attempt's lease, in Unix milliseconds, or zero.
	LeasingAt int64 `json:"leasingAt,omitempty"`
	// ExpiredAt is when the claim's expiry or release decided to return the
	// task to the queue, in Unix milliseconds, or zero. A claim with it set
	// is gone for every reader but the expiry, which finishes returning the
	// task.
	ExpiredAt int64 `json:"expiredAt,omitempty"`
	// RetiringAt is when a coordinator began to retire the task, which a
	// removal left with no worker, in Unix milliseconds, or zero. No worker
	// claims or accepts a task with it set.
	RetiringAt int64 `json:"retiringAt,omitempty"`
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
	"target_worker_id":              "TargetWorkerID",
	"attempt_id":                    "AttemptID",
	"attempt_key":                   "AttemptKey",
	"step":                          "Step",
	"params":                        "Params",
	"queue_name":                    "QueueName",
	"base_config":                   "BaseConfig",
	"base_config_workspace":         "BaseConfigWorkspace",
	"labels":                        "Labels",
	"schedule_time":                 "ScheduleTime",
	"source_file":                   "SourceFile",
	"worker_selector":               "WorkerSelector",
	"external_step_retry":           "ExternalStepRetry",
	"include_downstream":            "IncludeDownstream",
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

// candidatePendingIDs returns a copy of the matching pending IDs so callers
// may mutate the index while iterating. The copy is allocated lazily so a
// scan with no match costs nothing.
func (idx *dispatchTaskIndex) candidatePendingIDs(claim dispatch.DispatchTaskClaim) []string {
	var ids []string
	for i, id := range idx.pendingIDs {
		entry, ok := idx.pending[id]
		if !ok {
			continue
		}
		if entry.targetWorkerID != "" && entry.targetWorkerID != claim.WorkerID {
			continue
		}
		if entry.needsConnectionTokens && !claim.ConnectionTokens {
			continue
		}
		if !ir.MatchWorkerSelector(claim.Labels, entry.workerSelector) {
			continue
		}
		if ids == nil {
			ids = make([]string, 0, len(idx.pendingIDs)-i)
		}
		ids = append(ids, id)
	}
	return ids
}

func (idx *dispatchTaskIndex) hasExpired(now time.Time, ttl time.Duration) bool {
	if idx == nil {
		return false
	}
	for _, entry := range idx.claims {
		claimedAt := dispatchRecordTimestamp(entry.claimedAt, entry.updatedAt)
		if now.Sub(claimedAt) >= ttl {
			return true
		}
	}
	return false
}

// rememberNoMatch records that no pending task matches the claim shape key
// until the pending set changes. The cache is cleared when it reaches
// dispatchNoMatchCacheLimit entries.
func (idx *dispatchTaskIndex) rememberNoMatch(key string) {
	if idx == nil {
		return
	}
	if _, ok := idx.noMatch[key]; !ok && len(idx.noMatch) >= dispatchNoMatchCacheLimit {
		clear(idx.noMatch)
	}
	idx.noMatch[key] = struct{}{}
}

// hasNoMatch reports whether the claim shape key is known to match no
// pending task.
func (idx *dispatchTaskIndex) hasNoMatch(key string) bool {
	if idx == nil {
		return false
	}
	_, ok := idx.noMatch[key]
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
		entry.targetWorkerID = payload.Task.TargetWorkerID
		entry.workerSelector = maps.Clone(payload.Task.WorkerSelector)
		entry.needsConnectionTokens = payload.Task.NeedsConnectionTokens
		if !payload.Task.HoldUntil.IsZero() {
			entry.holdUntil = payload.Task.HoldUntil.UnixMilli()
		}
	}
	return entry
}

// holdExpired reports whether the wait of a held pending task has passed at
// now. Such a task is no longer claimable and waits for the coordinator to
// drop it.
func holdExpired(holdUntil time.Time, now time.Time) bool {
	return !holdUntil.IsZero() && !now.Before(holdUntil)
}

// dispatchNoMatchKey identifies a claim's worker, label set, and whether
// it takes connection tokens in the no-match cache. Every field is
// length-prefixed so boundaries stay unambiguous regardless of the bytes
// in the worker ID, keys, and values.
func dispatchNoMatchKey(claim dispatch.DispatchTaskClaim) string {
	workerID, labels := claim.WorkerID, claim.Labels
	workerIDLength := strconv.Itoa(len(workerID))
	keys := make([]string, 0, len(labels))
	size := len(workerIDLength) + 1 + len(workerID)
	for key, value := range labels {
		keys = append(keys, key)
		size += len(key) + len(value) + 8
	}
	sort.Strings(keys)
	var b strings.Builder
	b.Grow(size + 1)
	if claim.ConnectionTokens {
		b.WriteByte('t')
	} else {
		b.WriteByte('f')
	}
	b.WriteString(workerIDLength)
	b.WriteByte(':')
	b.WriteString(workerID)
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

// WithDispatchReservationTTL sets how long a claim can go unacknowledged before
// its task returns to pending, and how long a queue admission can go without
// execution evidence before cleanup releases it. A pending task never expires.
func WithDispatchReservationTTL(ttl time.Duration) DispatchTaskStoreOption {
	return func(store *DispatchTaskStore) {
		store.reservationTTL = normalizeDispatchReservationTTL(ttl)
	}
}

// WithDispatchAdmissionLiveness enables admission cleanup against shared
// distributed liveness stores. An expired claim whose attempt has a lease
// that the claim's acceptance wrote then goes without returning its task to
// the queue.
func WithDispatchAdmissionLiveness(
	leaseStore dispatch.DAGRunLeaseStore,
	activeRunStore dispatch.ActiveDistributedRunStore,
) DispatchTaskStoreOption {
	return func(store *DispatchTaskStore) {
		store.admissionLeaseStore = leaseStore
		store.admissionActiveRunStore = activeRunStore
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

func (s *DispatchTaskStore) withTransitionLock(
	ctx context.Context,
	fn func(context.Context) error,
) error {
	if s.transitionLock == nil {
		return fn(ctx)
	}
	return s.transitionLock(ctx, fn)
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
	return s.reconcileDispatchIndexIDs(ctx, now)
}

// reconcileDispatchIndexIDs rebuilds the index when the stored record IDs
// differ from the indexed ones, as after another coordinator's writes.
func (s *DispatchTaskStore) reconcileDispatchIndexIDs(ctx context.Context, now time.Time) (bool, error) {
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

func (s *DispatchTaskStore) Enqueue(ctx context.Context, task *dispatch.DispatchTask) error {
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
	write := func(lockCtx context.Context) error {
		if err := s.col.Put(lockCtx, rec); err != nil {
			return err
		}
		if s.index == nil {
			s.index = newDispatchTaskIndex()
		}
		s.index.addPending(rec, payload)
		return nil
	}
	if task.WorkspaceBundleDigest == "" {
		return write(ctx)
	}
	return s.withTransitionLock(ctx, write)
}

// ClaimNext atomically transitions one matching pending record into a
// claim. CompareAndDelete(pending) is the per-task atomicity point;
// concurrent pollers racing on the same pending see one winner and the
// losers clean up their orphan claim and continue to the next pending.
func (s *DispatchTaskStore) ClaimNext(ctx context.Context, claim dispatch.DispatchTaskClaim) (*dispatch.ClaimedDispatchTask, error) {
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
		if err != nil {
			return nil, err
		}
		if claimed != nil {
			_, _ = s.reconcileDispatchIndexIDsIfDue(ctx, time.Now().UTC())
			return claimed, nil
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

// claimNextPending claims the oldest indexed pending task that matches claim.
// The second result reports a stale index, which the caller rebuilds before
// trying again.
func (s *DispatchTaskStore) claimNextPending(ctx context.Context, claim dispatch.DispatchTaskClaim) (*dispatch.ClaimedDispatchTask, bool, error) {
	if s.index == nil {
		if err := s.rebuildDispatchIndex(ctx); err != nil {
			return nil, false, err
		}
	}
	// Idle pollers hit this path on every fallback tick; every change to the
	// pending set invalidates the no-match cache, so skipping it here is safe.
	if len(s.index.pendingIDs) == 0 {
		return nil, false, nil
	}
	noMatchKey := dispatchNoMatchKey(claim)
	if s.index.hasNoMatch(noMatchKey) {
		return nil, false, nil
	}
	ids := s.index.candidatePendingIDs(claim)
	if len(ids) == 0 {
		s.index.rememberNoMatch(noMatchKey)
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
		if holdExpired(payload.Task.HoldUntil, now) || payload.RetiringAt != 0 {
			// The coordinator fails the run and drops the task.
			s.index.addPending(rec, payload)
			continue
		}
		if payload.Task.TargetWorkerID != "" && payload.Task.TargetWorkerID != claim.WorkerID {
			s.index.addPending(rec, payload)
			continue
		}
		if !ir.MatchWorkerSelector(claim.Labels, payload.Task.WorkerSelector) {
			s.index.addPending(rec, payload)
			continue
		}
		if payload.Task.NeedsConnectionTokens && !claim.ConnectionTokens {
			s.index.addPending(rec, payload)
			continue
		}

		var claimed *dispatch.ClaimedDispatchTask
		var stale bool
		transition := func(lockCtx context.Context) error {
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
			if err := s.col.Put(lockCtx, claimRec); err != nil {
				return err
			}
			if err := s.col.CompareAndDelete(lockCtx, rec); err != nil {
				_ = s.col.CompareAndDelete(context.WithoutCancel(lockCtx), claimRec)
				if errors.Is(err, persis.ErrNotFound) || errors.Is(err, persis.ErrConflict) {
					s.index.removePending(id)
					stale = true
					return nil
				}
				return err
			}
			s.index.replacePendingWithClaim(id, claimRec, payload)

			claimed = &dispatch.ClaimedDispatchTask{
				Task:       cloneDispatchTask(task),
				ClaimToken: claimToken,
				ClaimedAt:  claimedAt,
				WorkerID:   claim.WorkerID,
				PollerID:   claim.PollerID,
				Owner:      claim.Owner,
			}
			return nil
		}
		if payload.Task.WorkspaceBundleDigest != "" {
			err = s.withTransitionLock(ctx, transition)
		} else {
			err = transition(ctx)
		}
		return claimed, stale, err
	}
	s.index.rememberNoMatch(noMatchKey)
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

func (s *DispatchTaskStore) GetClaim(ctx context.Context, claimToken string) (*dispatch.ClaimedDispatchTask, error) {
	rec, err := s.col.Get(ctx, claimDispatchRecordID(claimToken))
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, dispatch.ErrDispatchTaskNotFound
		}
		return nil, err
	}
	payload, err := dispatchTaskPayloadFromRecord(rec)
	if err != nil {
		return nil, err
	}
	if payload.Task == nil || payload.ClaimToken == "" || payload.ClaimToken != claimToken || payload.ClaimedAt == 0 || payload.ExpiredAt != 0 || payload.RetiringAt != 0 {
		return nil, dispatch.ErrDispatchTaskNotFound
	}
	return &dispatch.ClaimedDispatchTask{
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

	err := retryConflict(ctx, func(ctx context.Context) error {
		rec, err := s.col.Get(ctx, claimDispatchRecordID(claimToken))
		if err != nil {
			return err
		}
		payload, err := dispatchTaskPayloadFromRecord(rec)
		if err != nil {
			return err
		}
		// A claim that its expiry decided on, or that an acceptance marked,
		// is no longer this release's.
		if payload.Task == nil || payload.ClaimToken == "" || payload.ClaimToken != claimToken || payload.ClaimedAt == 0 ||
			payload.ExpiredAt != 0 || payload.LeasingAt != 0 || payload.RetiringAt != 0 {
			return dispatch.ErrDispatchTaskNotFound
		}
		// The release decides on the claim as its expiry does, so the two
		// never both queue the task and never both drop it.
		return s.withClaimTransition(ctx, payload, func(ctx context.Context) error {
			return s.expireClaim(ctx, rec, payload, time.Now().UTC())
		})
	})
	if errors.Is(err, persis.ErrNotFound) {
		return dispatch.ErrDispatchTaskNotFound
	}
	return err
}

func (s *DispatchTaskStore) DeleteClaim(ctx context.Context, claimToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.withTransitionLock(ctx, func(lockCtx context.Context) error {
		claimID := claimDispatchRecordID(claimToken)
		if err := s.col.Delete(lockCtx, claimID); err != nil && !errors.Is(err, persis.ErrNotFound) {
			return err
		}
		if s.index != nil {
			s.index.removeClaim(claimID)
		}
		return nil
	})
}

// ListBundleDigests returns bundle digests referenced by outstanding tasks.
func (s *DispatchTaskStore) ListBundleDigests(ctx context.Context) ([]string, error) {
	digests := make(map[string]struct{})

	// Cleanup holds the shared transition lock while taking this snapshot.
	for _, prefix := range []string{dispatchPendingPrefix, dispatchClaimsPrefix} {
		recs, err := s.listDispatchRecords(ctx, prefix)
		if err != nil {
			return nil, err
		}
		for _, rec := range recs {
			payload, err := dispatchTaskPayloadFromRecord(rec)
			if err != nil {
				return nil, err
			}
			if payload.Task == nil || payload.Task.WorkspaceBundleDigest == "" {
				continue
			}
			digests[payload.Task.WorkspaceBundleDigest] = struct{}{}
		}
	}

	result := make([]string, 0, len(digests))
	for digest := range digests {
		result = append(result, digest)
	}
	sort.Strings(result)
	return result, nil
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
	return s.indexedOutstandingAttempt(ctx, attemptKey)
}

// HasOutstandingAttemptUncached reports whether any pending or claimed record
// matches attemptKey, including records other coordinators wrote since the
// last periodic reconcile.
func (s *DispatchTaskStore) HasOutstandingAttemptUncached(ctx context.Context, attemptKey string) (bool, error) {
	if attemptKey == "" {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureDispatchIndex(ctx); err != nil {
		return false, err
	}
	// A record moves between pending and claims by writing the new record
	// before deleting the old one. One pass lists pending before claims, so
	// a second pass sees any record the first missed mid-move.
	for range 2 {
		if _, err := s.reconcileDispatchIndexIDs(ctx, time.Now().UTC()); err != nil {
			return false, err
		}
		outstanding, err := s.indexedOutstandingAttempt(ctx, attemptKey)
		if err != nil || outstanding {
			return outstanding, err
		}
	}
	return false, nil
}

func (s *DispatchTaskStore) indexedOutstandingAttempt(ctx context.Context, attemptKey string) (bool, error) {
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

func taskHoldUntil(payload dispatchTaskPayload) time.Time {
	if payload.Task == nil {
		return time.Time{}
	}
	return payload.Task.HoldUntil
}

// ListExpiredHeld returns the held pending tasks whose wait has passed at now.
// They stay in the store, unclaimable, until DeletePending removes them, so a
// coordinator that fails between failing the run and dropping the task finds
// the task again on its next pass.
func (s *DispatchTaskStore) ListExpiredHeld(ctx context.Context, now time.Time) ([]*dispatch.DispatchTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureDispatchIndex(ctx); err != nil {
		return nil, err
	}
	var expired []*dispatch.DispatchTask
	for _, id := range append([]string(nil), s.index.pendingIDs...) {
		entry, ok := s.index.pending[id]
		if !ok || entry.holdUntil == 0 || now.UnixMilli() < entry.holdUntil {
			continue
		}
		rec, err := s.col.Get(ctx, id)
		if err != nil {
			if errors.Is(err, persis.ErrNotFound) {
				s.index.removePending(id)
				continue
			}
			return nil, err
		}
		payload, err := dispatchTaskPayloadFromRecord(rec)
		if err != nil {
			return nil, err
		}
		if !holdExpired(taskHoldUntil(payload), now) {
			s.index.addPending(rec, payload)
			continue
		}
		expired = append(expired, cloneDispatchTask(payload.Task))
	}
	return expired, nil
}

// ListStalePending returns the pending tasks, held ones aside, enqueued before
// cutoff. They stay claimable until the coordinator drops the ones whose runs
// no longer await them.
func (s *DispatchTaskStore) ListStalePending(ctx context.Context, cutoff time.Time) ([]*dispatch.DispatchTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureDispatchIndex(ctx); err != nil {
		return nil, err
	}
	var stale []*dispatch.DispatchTask
	for _, id := range append([]string(nil), s.index.pendingIDs...) {
		entry, ok := s.index.pending[id]
		if !ok || !entry.hasTask || entry.holdUntil != 0 ||
			!dispatchRecordTimestamp(entry.enqueuedAt, entry.createdAt).Before(cutoff) {
			continue
		}
		rec, err := s.col.Get(ctx, id)
		if err != nil {
			if errors.Is(err, persis.ErrNotFound) {
				s.index.removePending(id)
				continue
			}
			return nil, err
		}
		payload, err := dispatchTaskPayloadFromRecord(rec)
		if err != nil {
			return nil, err
		}
		if payload.Task == nil || !payload.Task.HoldUntil.IsZero() ||
			!dispatchRecordTimestamp(payload.EnqueuedAt, rec.CreatedAt).Before(cutoff) {
			s.index.addPending(rec, payload)
			continue
		}
		stale = append(stale, cloneDispatchTask(payload.Task))
	}
	return stale, nil
}

func (s *DispatchTaskStore) ListBoundTo(ctx context.Context, workerIDs []string) ([]dispatch.BoundDispatch, error) {
	if len(workerIDs) == 0 {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureDispatchIndex(ctx); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if _, err := s.reconcileDispatchIndexIDs(ctx, now); err != nil {
		return nil, err
	}
	// A bound claim past its timeout goes back to the queue first, as its
	// expiry decides, so a claim whose acceptance died is found as pending.
	for _, id := range s.boundEntryIDs(s.index.claims, workerIDs) {
		if err := s.recycleClaim(ctx, id, now); err != nil {
			return nil, err
		}
	}
	var bound []dispatch.BoundDispatch
	for _, ids := range [][]string{s.boundEntryIDs(s.index.pending, workerIDs), s.boundEntryIDs(s.index.claims, workerIDs)} {
		for _, id := range ids {
			rec, err := s.col.Get(ctx, id)
			if errors.Is(err, persis.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			payload, err := dispatchTaskPayloadFromRecord(rec)
			if err != nil {
				return nil, err
			}
			if payload.Task == nil || !slices.Contains(workerIDs, payload.Task.TargetWorkerID) ||
				payload.LeasingAt != 0 || payload.ExpiredAt != 0 {
				continue
			}
			task := cloneDispatchTask(payload.Task)
			task.ClaimToken = payload.ClaimToken
			bound = append(bound, dispatch.BoundDispatch{Task: task, Retiring: payload.RetiringAt != 0})
		}
	}
	return bound, nil
}

// boundEntryIDs returns the IDs of the entries whose task is bound to one
// of workerIDs.
func (s *DispatchTaskStore) boundEntryIDs(entries map[string]dispatchTaskIndexEntry, workerIDs []string) []string {
	var ids []string
	for id, entry := range entries {
		if entry.hasTask && slices.Contains(workerIDs, entry.targetWorkerID) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func (s *DispatchTaskStore) MarkRetiring(ctx context.Context, task *dispatch.DispatchTask) error {
	return s.retireBound(ctx, task, func(ctx context.Context, rec *persis.Record, payload dispatchTaskPayload) error {
		if payload.RetiringAt != 0 {
			return nil
		}
		payload.RetiringAt = time.Now().UTC().UnixMilli()
		next, err := s.newDispatchRecord(rec.ID, payload, rec.CreatedAt, time.Now().UTC())
		if err != nil {
			return err
		}
		return s.col.CompareAndSwap(ctx, rec.ID, rec.Data, next.Data)
	}, false)
}

func (s *DispatchTaskStore) DeleteRetiring(ctx context.Context, task *dispatch.DispatchTask) error {
	err := s.retireBound(ctx, task, func(ctx context.Context, rec *persis.Record, payload dispatchTaskPayload) error {
		if payload.RetiringAt == 0 {
			return dispatch.ErrDispatchTaskNotFound
		}
		return s.col.CompareAndDelete(ctx, rec)
	}, true)
	if errors.Is(err, dispatch.ErrDispatchTaskNotFound) {
		return nil
	}
	return err
}

// retireBound applies write to the record of task, its claim when it has a
// claim token, else its pending records, each from the version it reads
// while no worker claimed or began to accept it. It returns
// [dispatch.ErrDispatchTaskNotFound] when write applied to none. With
// deleted, a record that write applied to leaves the index.
func (s *DispatchTaskStore) retireBound(ctx context.Context, task *dispatch.DispatchTask, write func(context.Context, *persis.Record, dispatchTaskPayload) error, deleted bool) error {
	if task == nil {
		return dispatch.ErrDispatchTaskNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureDispatchIndex(ctx); err != nil {
		return err
	}
	var ids []string
	if task.ClaimToken != "" {
		ids = []string{claimDispatchRecordID(task.ClaimToken)}
	} else {
		for id, entry := range s.index.pending {
			if entry.attemptKey == task.AttemptKey && entry.targetWorkerID == task.TargetWorkerID {
				ids = append(ids, id)
			}
		}
	}
	applied := false
	for _, id := range ids {
		err := retryConflict(ctx, func(ctx context.Context) error {
			rec, err := s.col.Get(ctx, id)
			if err != nil {
				return err
			}
			payload, err := dispatchTaskPayloadFromRecord(rec)
			if err != nil {
				return err
			}
			if payload.Task == nil || payload.Task.TargetWorkerID != task.TargetWorkerID || payload.ClaimToken != task.ClaimToken ||
				payload.LeasingAt != 0 || payload.ExpiredAt != 0 {
				return dispatch.ErrDispatchTaskNotFound
			}
			return s.withClaimTransition(ctx, payload, func(ctx context.Context) error {
				return write(ctx, rec, payload)
			})
		})
		switch {
		case err == nil:
			applied = true
			switch {
			case !deleted:
			case task.ClaimToken != "":
				s.index.removeClaim(id)
			default:
				s.index.removePending(id)
			}
		case errors.Is(err, persis.ErrNotFound), errors.Is(err, dispatch.ErrDispatchTaskNotFound):
		default:
			return err
		}
	}
	if !applied {
		return dispatch.ErrDispatchTaskNotFound
	}
	return nil
}

// DeletePending removes the pending tasks of an attempt. A task a worker has
// already claimed is not touched.
func (s *DispatchTaskStore) DeletePending(ctx context.Context, attemptKey string) error {
	if attemptKey == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureDispatchIndex(ctx); err != nil {
		return err
	}
	for _, id := range append([]string(nil), s.index.pendingIDs...) {
		entry, ok := s.index.pending[id]
		if !ok || entry.attemptKey != attemptKey {
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

func (s *DispatchTaskStore) recycleExpiredReservations(ctx context.Context) error {
	if s.index == nil {
		if err := s.rebuildDispatchIndex(ctx); err != nil {
			return err
		}
	}
	if err := s.recycleExpiredClaims(ctx); err != nil {
		return err
	}
	return s.removePendingRecordsWithActiveClaims(ctx)
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
		if err := s.recycleClaim(ctx, id, now); err != nil {
			return err
		}
	}
	return nil
}

// recycleClaim returns the task of the claim id to the queue when the
// claim is past its timeout at now, as its expiry decides.
func (s *DispatchTaskStore) recycleClaim(ctx context.Context, id string, now time.Time) error {
	entry, ok := s.index.claims[id]
	if !ok {
		return nil
	}
	claimedAt := dispatchRecordTimestamp(entry.claimedAt, entry.updatedAt)
	if now.Sub(claimedAt) < s.reservationTTL {
		return nil
	}

	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			s.index.removeClaim(id)
			return nil
		}
		return err
	}
	payload, err := dispatchTaskPayloadFromRecord(rec)
	if err != nil {
		return err
	}
	claimedAt = dispatchRecordTimestamp(payload.ClaimedAt, rec.UpdatedAt)
	// A claim being retired is the retirement's to finish.
	if now.Sub(claimedAt) < s.reservationTTL || payload.RetiringAt != 0 {
		s.index.addClaim(rec, payload)
		return nil
	}

	// A claim that an acceptance marked before writing the attempt's
	// lease never returns to the queue while the mark is fresh, and its
	// lease, once written, is the stale-lease check's to end. A mark as
	// old as the claim timeout is of an acceptance that died: the claim
	// returns to the queue only when no lease came of it. The release
	// below deletes the claim from the version read here, so a mark
	// written meanwhile keeps it.
	if payload.LeasingAt != 0 {
		if now.Sub(time.UnixMilli(payload.LeasingAt)) < s.reservationTTL {
			s.index.addClaim(rec, payload)
			return nil
		}
		leased, err := s.claimLeased(ctx, payload)
		if err != nil {
			// Only this claim waits for a lease that can be read.
			logger.Warn(ctx, "Failed to read the lease of an expired claim", tag.AttemptKey(payload.Task.AttemptKey), tag.Error(err))
			s.index.addClaim(rec, payload)
			return nil
		}
		if leased {
			if err := s.col.CompareAndDelete(ctx, rec); err != nil && !errors.Is(err, persis.ErrNotFound) && !errors.Is(err, persis.ErrConflict) {
				return err
			}
			s.index.removeClaim(id)
			return nil
		}
	}

	if err := s.withClaimTransition(ctx, payload, func(ctx context.Context) error {
		return s.expireClaim(ctx, rec, payload, now)
	}); err != nil {
		switch {
		case errors.Is(err, persis.ErrNotFound):
			s.index.removeClaim(id)
			return nil
		case errors.Is(err, persis.ErrConflict):
			// The claim changed since the read, as when an acceptance
			// marked it; a later pass reads it again.
			return nil
		}
		return err
	}
	return nil
}

// expireClaim returns the task of the claim rec, expired or released, to
// the queue. It
// first records the decision on the claim, in one compare-and-swap from
// the version read, so an acceptance that marks the claim either comes
// first and keeps it, or finds it gone. Only then is the task claimable
// again, and the claim is deleted. A claim that already holds the decision
// is of an expiry that stopped after it, and this finishes that expiry.
// The queued record and its enqueue time come from the decision, so a
// repeat writes the same record. A repeat after another worker claimed
// that record queues the task again behind that claim, which the pass that
// removes pending records of live claims then drops.
func (s *DispatchTaskStore) expireClaim(ctx context.Context, rec *persis.Record, payload dispatchTaskPayload, now time.Time) error {
	if payload.ExpiredAt == 0 {
		payload.ExpiredAt = now.UnixMilli()
		expired, err := s.newDispatchRecord(rec.ID, payload, rec.CreatedAt, now)
		if err != nil {
			return err
		}
		if err := s.col.CompareAndSwap(ctx, rec.ID, rec.Data, expired.Data); err != nil {
			return err
		}
		rec = expired
	}
	pendingRec, queued, err := s.requeuedRecord(payload, time.UnixMilli(payload.ExpiredAt).UTC())
	if err != nil {
		return err
	}
	if err := s.col.Create(ctx, pendingRec); err != nil && !errors.Is(err, persis.ErrConflict) {
		return err
	}
	if err := s.col.CompareAndDelete(ctx, rec); err != nil && !errors.Is(err, persis.ErrNotFound) {
		return err
	}
	if s.index != nil {
		s.index.replaceClaimWithPending(rec.ID, pendingRec, queued)
	}
	return nil
}

// withClaimTransition runs transition, under the transition lock when the
// claim's task carries a workspace bundle.
func (s *DispatchTaskStore) withClaimTransition(ctx context.Context, payload dispatchTaskPayload, transition func(context.Context) error) error {
	if payload.Task == nil || payload.Task.WorkspaceBundleDigest == "" {
		return transition(ctx)
	}
	return s.withTransitionLock(ctx, transition)
}

// MarkClaimLeasing marks the claim claimToken as having an acceptance that
// writes the attempt's lease, in one compare-and-swap on the claim, so the
// claim's expiry cannot return its task to the queue from a read taken
// before. It returns [dispatch.ErrDispatchTaskNotFound] when the claim is
// gone.
func (s *DispatchTaskStore) MarkClaimLeasing(ctx context.Context, claimToken string) error {
	id := claimDispatchRecordID(claimToken)
	return retryConflict(ctx, func(ctx context.Context) error {
		rec, err := s.col.Get(ctx, id)
		if errors.Is(err, persis.ErrNotFound) {
			return dispatch.ErrDispatchTaskNotFound
		}
		if err != nil {
			return err
		}
		payload, err := dispatchTaskPayloadFromRecord(rec)
		if err != nil {
			return err
		}
		if payload.ExpiredAt != 0 || payload.RetiringAt != 0 {
			return dispatch.ErrDispatchTaskNotFound
		}
		if payload.LeasingAt != 0 {
			return nil
		}
		payload.LeasingAt = time.Now().UTC().UnixMilli()
		next, err := s.newDispatchRecord(id, payload, rec.CreatedAt, time.Now().UTC())
		if err != nil {
			return err
		}
		err = s.col.CompareAndSwap(ctx, id, rec.Data, next.Data)
		if errors.Is(err, persis.ErrNotFound) {
			return dispatch.ErrDispatchTaskNotFound
		}
		return err
	})
}

// claimLeased reports whether the attempt of the claim in payload has a
// lease that the claim's acceptance wrote. Without a lease store it
// reports false.
func (s *DispatchTaskStore) claimLeased(ctx context.Context, payload dispatchTaskPayload) (bool, error) {
	if s.admissionLeaseStore == nil || payload.Task == nil || payload.Task.AttemptKey == "" || payload.ClaimToken == "" {
		return false, nil
	}
	lease, err := s.admissionLeaseStore.Get(ctx, payload.Task.AttemptKey)
	if errors.Is(err, dispatch.ErrDAGRunLeaseNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return lease.ClaimToken == payload.ClaimToken, nil
}

// requeuedRecord returns the pending record that queues the task of the
// claim in payload again at now, and its payload.
func (s *DispatchTaskStore) requeuedRecord(payload dispatchTaskPayload, now time.Time) (*persis.Record, dispatchTaskPayload, error) {
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
	payload.Owner = dispatch.CoordinatorEndpoint{}
	payload.LeasingAt = 0
	payload.ExpiredAt = 0
	payload.Task = clearDispatchTaskClaim(payload.Task)

	pendingID, err := pendingDispatchRecordID(payload.TaskFileName)
	if err != nil {
		return nil, dispatchTaskPayload{}, err
	}
	pendingRec, err := s.newDispatchRecord(pendingID, payload, releasedAt, releasedAt)
	if err != nil {
		return nil, dispatchTaskPayload{}, err
	}
	return pendingRec, payload, nil
}

// removePendingRecordsWithActiveClaims deletes pending records whose task is
// already held by a live claim, so a recycled task is never dispatched twice.
func (s *DispatchTaskStore) removePendingRecordsWithActiveClaims(ctx context.Context) error {
	if s.index == nil || len(s.index.claims) == 0 {
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
	if idCol, ok := s.col.(recordIDsCollection); ok {
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

func legacyDispatchTaskFromRecord(data []byte) (*dispatch.DispatchTask, error) {
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
	var task dispatch.DispatchTask
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
	var decoded ir.DAGRunStatus
	if err := json.Unmarshal([]byte(status.JSONData), &decoded); err != nil {
		return nil, fmt.Errorf("decode previous status: %w", err)
	}
	return json.RawMessage(status.JSONData), nil
}

func legacyOwnerJSON(fields map[string]json.RawMessage) (json.RawMessage, bool, error) {
	var owner dispatch.CoordinatorEndpoint
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

func cloneDispatchTask(task *dispatch.DispatchTask) *dispatch.DispatchTask {
	if task == nil {
		return nil
	}
	cloned := *task
	cloned.WorkerSelector = maps.Clone(task.WorkerSelector)
	return &cloned
}

func applyDispatchTaskClaim(task *dispatch.DispatchTask, owner dispatch.CoordinatorEndpoint, claimToken string) (*dispatch.DispatchTask, error) {
	task = cloneDispatchTask(task)
	if task == nil {
		return nil, nil
	}
	task.Owner = owner
	task.ClaimToken = claimToken
	return task, nil
}

func clearDispatchTaskClaim(task *dispatch.DispatchTask) *dispatch.DispatchTask {
	task = cloneDispatchTask(task)
	if task == nil {
		return nil
	}
	task.Owner = dispatch.CoordinatorEndpoint{}
	task.ClaimToken = ""
	task.WorkerID = ""
	return task
}
