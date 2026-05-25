// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis"
)

const (
	dagRunStoreVersion = 1

	dagRunRootPrefix  = "runs/"
	dagRunSubPrefix   = "children/"
	dagRunAttemptPart = "attempts/"
)

// ErrDAGRunIDEmpty is returned when an operation requires a dag-run ID.
var ErrDAGRunIDEmpty = errors.New("dag-run ID is empty")

// DAGRunStoreOption configures a collection-backed DAGRunStore.
type DAGRunStoreOption func(*DAGRunStore)

// WithDAGRunLatestStatusToday controls whether LatestAttempt only considers
// runs created during the current day in the configured location.
func WithDAGRunLatestStatusToday(latestStatusToday bool) DAGRunStoreOption {
	return func(s *DAGRunStore) {
		s.latestStatusToday = latestStatusToday
	}
}

// WithDAGRunLocation sets the timezone used by latest-status-today filtering.
func WithDAGRunLocation(location *time.Location) DAGRunStoreOption {
	return func(s *DAGRunStore) {
		if location != nil {
			s.location = location
		}
	}
}

// DAGRunStore implements [exec.DAGRunStore] on top of a [persis.Collection].
// It stores only control-plane metadata; local execution artifacts remain
// outside the backend until the artifact/log story is ported separately.
type DAGRunStore struct {
	col               persis.Collection
	latestStatusToday bool
	location          *time.Location
	mu                sync.Mutex
}

var _ exec.DAGRunStore = (*DAGRunStore)(nil)

type dagRunPayload struct {
	Version          int                          `json:"version"`
	Name             string                       `json:"name"`
	DAGRunID         string                       `json:"dagRunId"`
	AttemptID        string                       `json:"attemptId"`
	Root             exec.DAGRunRef               `json:"root,omitzero"`
	Parent           exec.DAGRunRef               `json:"parent,omitzero"`
	RunCreatedAt     int64                        `json:"runCreatedAt"`
	AttemptCreatedAt int64                        `json:"attemptCreatedAt"`
	DAG              *core.DAG                    `json:"dag,omitempty"`
	Status           *exec.DAGRunStatus           `json:"status,omitempty"`
	Hidden           bool                         `json:"hidden,omitempty"`
	AbortRequested   bool                         `json:"abortRequested,omitempty"`
	Outputs          *exec.DAGRunOutputs          `json:"outputs,omitempty"`
	StepMessages     map[string][]exec.LLMMessage `json:"stepMessages,omitempty"`
}

// NewDAGRunStore creates a collection-backed DAG-run store.
func NewDAGRunStore(col persis.Collection, opts ...DAGRunStoreOption) *DAGRunStore {
	s := &DAGRunStore{
		col:               col,
		latestStatusToday: true,
		location:          time.Local,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *DAGRunStore) CreateAttempt(ctx context.Context, dag *core.DAG, ts time.Time, dagRunID string, opts exec.NewDAGRunAttemptOptions) (exec.DAGRunAttempt, error) {
	if dagRunID == "" {
		return nil, ErrDAGRunIDEmpty
	}
	if dag == nil || dag.Name == "" {
		return nil, fmt.Errorf("dag-run store: DAG name is required")
	}
	if opts.RootDAGRun != nil {
		return s.createSubAttempt(ctx, dag, ts, dagRunID, opts)
	}

	var attempt *DAGRunAttempt
	err := s.withLock(ctx, dagRunLockKey(dag.Name, dagRunID), func() error {
		records, err := s.recordsForRootRun(ctx, dag.Name, dagRunID)
		if err != nil {
			return err
		}
		if len(records) > 0 && !opts.Retry {
			return fmt.Errorf("%w: %s", exec.ErrDAGRunAlreadyExists, dagRunID)
		}
		if len(records) == 0 && opts.Retry {
			return fmt.Errorf("failed to find execution: %w", exec.ErrDAGRunIDNotFound)
		}

		attemptID, err := normalizeOrGenerateAttemptID(opts.AttemptID)
		if err != nil {
			return err
		}
		runCreatedAt := ts.UTC()
		if len(records) > 0 {
			runCreatedAt = dagRunCreatedAtFromPayloads(records)
		}
		recordID := rootAttemptRecordID(dag.Name, dagRunID, attemptID)
		if err := s.ensureAttemptRecordDoesNotExist(ctx, recordID, attemptID); err != nil {
			return err
		}

		payload := dagRunPayload{
			Version:          dagRunStoreVersion,
			Name:             dag.Name,
			DAGRunID:         dagRunID,
			AttemptID:        attemptID,
			RunCreatedAt:     runCreatedAt.UnixNano(),
			AttemptCreatedAt: ts.UTC().UnixNano(),
			DAG:              cloneDAG(dag),
		}
		if opts.Retry {
			payload.StepMessages = stepMessagesFromPayloads(records)
		}
		if err := s.putPayload(ctx, recordID, payload, ts.UTC(), ts.UTC()); err != nil {
			return err
		}
		attempt = &DAGRunAttempt{store: s, id: attemptID, recordID: recordID, dag: cloneDAG(dag)}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return attempt, nil
}

func (s *DAGRunStore) RecentAttempts(ctx context.Context, name string, itemLimit int) []exec.DAGRunAttempt {
	if itemLimit <= 0 {
		itemLimit = 10
	}
	attempts, err := s.latestRootAttempts(ctx, name)
	if err != nil {
		return nil
	}
	if len(attempts) > itemLimit {
		attempts = attempts[:itemLimit]
	}
	out := make([]exec.DAGRunAttempt, 0, len(attempts))
	for _, item := range attempts {
		out = append(out, s.attemptFromRecord(item.record, item.payload))
	}
	return out
}

func (s *DAGRunStore) LatestAttempt(ctx context.Context, dagName string) (exec.DAGRunAttempt, error) {
	attempts, err := s.latestRootAttempts(ctx, dagName)
	if err != nil {
		return nil, err
	}
	if s.latestStatusToday {
		start := startOfToday(s.location)
		filtered := attempts[:0]
		for _, item := range attempts {
			if time.Unix(0, item.payload.RunCreatedAt).UTC().Before(start) {
				continue
			}
			filtered = append(filtered, item)
		}
		attempts = filtered
	}
	if len(attempts) == 0 {
		return nil, exec.ErrNoStatusData
	}
	return s.attemptFromRecord(attempts[0].record, attempts[0].payload), nil
}

func (s *DAGRunStore) ListStatuses(ctx context.Context, opts ...exec.ListDAGRunStatusesOption) ([]*exec.DAGRunStatus, error) {
	options, err := prepareDAGRunListOptions(opts)
	if err != nil {
		return nil, err
	}
	statuses, _, err := s.listStatuses(ctx, options, options.Limit, false)
	return statuses, err
}

func (s *DAGRunStore) ListStatusesPage(ctx context.Context, opts ...exec.ListDAGRunStatusesOption) (exec.DAGRunStatusPage, error) {
	options, err := prepareDAGRunListOptions(opts)
	if err != nil {
		return exec.DAGRunStatusPage{}, err
	}
	statuses, nextCursor, err := s.listStatuses(ctx, options, options.Limit, true)
	if err != nil {
		return exec.DAGRunStatusPage{}, err
	}
	return exec.DAGRunStatusPage{Items: statuses, NextCursor: nextCursor}, nil
}

func (s *DAGRunStore) CompareAndSwapLatestAttemptStatus(
	ctx context.Context,
	dagRun exec.DAGRunRef,
	expectedAttemptID string,
	expectedStatus core.Status,
	mutate func(*exec.DAGRunStatus) error,
	opts ...exec.CompareAndSwapStatusOption,
) (*exec.DAGRunStatus, bool, error) {
	if dagRun.ID == "" {
		return nil, false, ErrDAGRunIDEmpty
	}
	cfg := exec.NewCompareAndSwapStatusOptions(opts...)
	rootRef := cfg.RootDAGRun
	if rootRef.Zero() {
		rootRef = dagRun
	}
	if rootRef.Name == "" {
		rootRef.Name = dagRun.Name
	}
	if rootRef.ID == "" {
		return nil, false, ErrDAGRunIDEmpty
	}

	var current *exec.DAGRunStatus
	var swapped bool
	err := s.withLock(ctx, dagRunLockKey(rootRef.Name, rootRef.ID), func() error {
		item, err := s.latestAttemptItem(ctx, rootRef, dagRun)
		if err != nil {
			return err
		}
		if item.payload.Status == nil {
			return exec.ErrNoStatusData
		}
		status := cloneDAGRunStatus(item.payload.Status)
		current = cloneDAGRunStatus(status)
		if expectedAttemptID != "" && status.AttemptID != expectedAttemptID {
			return nil
		}
		if cfg.ExpectedAttemptKey != "" && status.AttemptKey != cfg.ExpectedAttemptKey {
			return nil
		}
		if status.DAGRunID != "" && status.DAGRunID != dagRun.ID {
			return nil
		}
		if status.Status != expectedStatus {
			return nil
		}
		if err := mutate(status); err != nil {
			return err
		}
		item.payload.Status = status
		nextData, _, err := persis.Encode(item.payload)
		if err != nil {
			return err
		}
		if err := s.col.CompareAndSwap(ctx, item.record.ID, item.record.Data, nextData); err != nil {
			if errors.Is(err, persis.ErrConflict) {
				return nil
			}
			return err
		}
		current = cloneDAGRunStatus(status)
		swapped = true
		return nil
	})
	return current, swapped, err
}

func (s *DAGRunStore) FindAttempt(ctx context.Context, ref exec.DAGRunRef) (exec.DAGRunAttempt, error) {
	if ref.ID == "" {
		return nil, ErrDAGRunIDEmpty
	}
	item, err := s.latestRootAttemptForRun(ctx, ref.Name, ref.ID)
	if err != nil {
		return nil, err
	}
	return s.attemptFromRecord(item.record, item.payload), nil
}

func (s *DAGRunStore) FindSubAttempt(ctx context.Context, ref exec.DAGRunRef, subDAGRunID string) (exec.DAGRunAttempt, error) {
	if ref.ID == "" {
		return nil, ErrDAGRunIDEmpty
	}
	item, err := s.latestSubAttemptForRun(ctx, ref, exec.NewDAGRunRef("", subDAGRunID))
	if err != nil {
		return nil, fmt.Errorf("failed to find sub dag-run: %w", err)
	}
	return s.attemptFromRecord(item.record, item.payload), nil
}

func (s *DAGRunStore) CreateSubAttempt(ctx context.Context, rootRef exec.DAGRunRef, subDAGRunID string) (exec.DAGRunAttempt, error) {
	if rootRef.ID == "" {
		return nil, ErrDAGRunIDEmpty
	}
	return s.createSubAttempt(ctx, nil, time.Now().UTC(), subDAGRunID, exec.NewDAGRunAttemptOptions{RootDAGRun: &rootRef})
}

func (s *DAGRunStore) RemoveOldDAGRuns(ctx context.Context, name string, retentionDays int, opts ...exec.RemoveOldDAGRunsOption) ([]string, error) {
	var options exec.RemoveOldDAGRunsOptions
	for _, opt := range opts {
		opt(&options)
	}
	if retentionDays < 0 && options.RetentionRuns == nil {
		return nil, nil
	}
	if options.RetentionRuns != nil && *options.RetentionRuns <= 0 {
		return nil, nil
	}

	attempts, err := s.latestRootAttempts(ctx, name)
	if err != nil {
		return nil, err
	}
	keep := make(map[string]struct{})
	if options.RetentionRuns != nil {
		for i, item := range attempts {
			if i >= *options.RetentionRuns {
				break
			}
			keep[item.payload.DAGRunID] = struct{}{}
		}
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
	var removed []string
	seen := make(map[string]struct{})
	for _, item := range attempts {
		if _, ok := seen[item.payload.DAGRunID]; ok {
			continue
		}
		seen[item.payload.DAGRunID] = struct{}{}
		if _, ok := keep[item.payload.DAGRunID]; ok {
			continue
		}
		if item.payload.Status != nil && item.payload.Status.Status.IsActive() {
			continue
		}
		if options.RetentionRuns == nil && retentionDays > 0 && !dagRunAttemptActivity(item).Before(cutoff) {
			continue
		}
		removed = append(removed, item.payload.DAGRunID)
		if options.DryRun {
			continue
		}
		if err := s.deleteRootRun(ctx, name, item.payload.DAGRunID); err != nil {
			return nil, err
		}
	}
	return removed, nil
}

func (s *DAGRunStore) RenameDAGRuns(ctx context.Context, oldName, newName string) error {
	if oldName == "" || newName == "" {
		return fmt.Errorf("dag-run store: old and new names are required")
	}
	if oldName == newName {
		return nil
	}
	return s.withLock(ctx, "rename/"+escapeDAGRunComponent(oldName), func() error {
		items, err := s.listAttemptItems(ctx, dagRunRootPrefix+escapeDAGRunComponent(oldName)+"/")
		if err != nil {
			return err
		}
		ops := make([]dagRunRenameOp, 0, len(items))
		for _, item := range items {
			next, newID := renameDAGRunPayload(item.payload, oldName, newName)
			ops = append(ops, dagRunRenameOp{
				oldID:     item.record.ID,
				newID:     newID,
				payload:   next,
				createdAt: item.record.CreatedAt,
				updatedAt: time.Now().UTC(),
			})
		}
		for _, op := range ops {
			if err := s.ensureRecordDoesNotExist(ctx, op.newID); err != nil {
				return err
			}
		}
		for _, op := range ops {
			if err := s.putPayload(ctx, op.newID, op.payload, op.createdAt, op.updatedAt); err != nil {
				return err
			}
			if err := s.col.Delete(ctx, op.oldID); err != nil && !errors.Is(err, persis.ErrNotFound) {
				return err
			}
		}
		return nil
	})
}

func (s *DAGRunStore) RemoveDAGRun(ctx context.Context, dagRun exec.DAGRunRef, opts ...exec.RemoveDAGRunOption) error {
	if dagRun.ID == "" {
		return ErrDAGRunIDEmpty
	}
	var options exec.RemoveDAGRunOptions
	for _, opt := range opts {
		opt(&options)
	}
	return s.withLock(ctx, dagRunLockKey(dagRun.Name, dagRun.ID), func() error {
		if options.RejectActive {
			item, err := s.latestRootAttemptForRun(ctx, dagRun.Name, dagRun.ID)
			if err != nil {
				return fmt.Errorf("failed to find latest attempt for dag-run %s: %w", dagRun.ID, err)
			}
			if item.payload.Status == nil {
				return fmt.Errorf("failed to read dag-run %s status: %w", dagRun.ID, exec.ErrNoStatusData)
			}
			if item.payload.Status.Status.IsActive() {
				return fmt.Errorf("%w: %s", exec.ErrDAGRunActive, item.payload.Status.Status.String())
			}
		}
		return s.deleteRootRun(ctx, dagRun.Name, dagRun.ID)
	})
}

func (s *DAGRunStore) createSubAttempt(ctx context.Context, dag *core.DAG, ts time.Time, subDAGRunID string, opts exec.NewDAGRunAttemptOptions) (exec.DAGRunAttempt, error) {
	if subDAGRunID == "" {
		return nil, ErrDAGRunIDEmpty
	}
	rootRef := opts.RootDAGRun
	if rootRef == nil || rootRef.ID == "" {
		return nil, ErrDAGRunIDEmpty
	}
	var attempt *DAGRunAttempt
	err := s.withLock(ctx, dagRunLockKey(rootRef.Name, rootRef.ID), func() error {
		rootRecords, err := s.recordsForRootRun(ctx, rootRef.Name, rootRef.ID)
		if err != nil {
			return err
		}
		if len(rootRecords) == 0 {
			return fmt.Errorf("failed to find root dag-run: %w", exec.ErrDAGRunIDNotFound)
		}
		records, err := s.recordsForSubRun(ctx, *rootRef, subDAGRunID)
		if err != nil {
			return err
		}
		if len(records) > 0 && !opts.Retry {
			return fmt.Errorf("%w: %s", exec.ErrDAGRunAlreadyExists, subDAGRunID)
		}
		if len(records) == 0 && opts.Retry {
			return fmt.Errorf("failed to find sub dag-run record: %w", exec.ErrDAGRunIDNotFound)
		}
		attemptID, err := normalizeOrGenerateAttemptID(opts.AttemptID)
		if err != nil {
			return err
		}
		runCreatedAt := ts.UTC()
		if len(records) > 0 {
			runCreatedAt = dagRunCreatedAtFromPayloads(records)
		}
		name := subDAGRunID
		if dag != nil && dag.Name != "" {
			name = dag.Name
		}
		recordID := subAttemptRecordID(*rootRef, subDAGRunID, attemptID)
		if err := s.ensureAttemptRecordDoesNotExist(ctx, recordID, attemptID); err != nil {
			return err
		}
		payload := dagRunPayload{
			Version:          dagRunStoreVersion,
			Name:             name,
			DAGRunID:         subDAGRunID,
			AttemptID:        attemptID,
			Root:             *rootRef,
			Parent:           *rootRef,
			RunCreatedAt:     runCreatedAt.UnixNano(),
			AttemptCreatedAt: ts.UTC().UnixNano(),
			DAG:              cloneDAG(dag),
		}
		if opts.Retry {
			payload.StepMessages = stepMessagesFromPayloads(records)
		}
		if err := s.putPayload(ctx, recordID, payload, ts.UTC(), ts.UTC()); err != nil {
			return err
		}
		attempt = &DAGRunAttempt{store: s, id: attemptID, recordID: recordID, dag: cloneDAG(dag)}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return attempt, nil
}

type dagRunRecordItem struct {
	record  *persis.Record
	payload dagRunPayload
}

type dagRunRenameOp struct {
	oldID     string
	newID     string
	payload   dagRunPayload
	createdAt time.Time
	updatedAt time.Time
}

func (s *DAGRunStore) latestRootAttempts(ctx context.Context, name string) ([]dagRunRecordItem, error) {
	items, err := s.rootAttemptItems(ctx, name)
	if err != nil {
		return nil, err
	}
	latestByRun := make(map[string]dagRunRecordItem)
	for _, item := range items {
		if item.payload.Hidden {
			continue
		}
		current, ok := latestByRun[item.payload.DAGRunID]
		if !ok || compareAttemptPayload(item.payload, current.payload) > 0 {
			latestByRun[item.payload.DAGRunID] = item
		}
	}
	out := make([]dagRunRecordItem, 0, len(latestByRun))
	for _, item := range latestByRun {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return compareRunPayload(out[i].payload, out[j].payload) < 0
	})
	return out, nil
}

func (s *DAGRunStore) latestRootAttemptForRun(ctx context.Context, name, dagRunID string) (dagRunRecordItem, error) {
	records, err := s.recordsForRootRun(ctx, name, dagRunID)
	if err != nil {
		return dagRunRecordItem{}, err
	}
	if len(records) == 0 {
		return dagRunRecordItem{}, exec.ErrDAGRunIDNotFound
	}
	item, ok := latestVisibleAttempt(records)
	if !ok {
		return dagRunRecordItem{}, exec.ErrNoStatusData
	}
	return item, nil
}

func (s *DAGRunStore) latestSubAttemptForRun(ctx context.Context, root exec.DAGRunRef, sub exec.DAGRunRef) (dagRunRecordItem, error) {
	records, err := s.recordsForSubRun(ctx, root, sub.ID)
	if err != nil {
		return dagRunRecordItem{}, err
	}
	if len(records) == 0 {
		return dagRunRecordItem{}, exec.ErrDAGRunIDNotFound
	}
	item, ok := latestVisibleAttempt(records)
	if !ok {
		return dagRunRecordItem{}, exec.ErrNoStatusData
	}
	return item, nil
}

func (s *DAGRunStore) latestAttemptItem(ctx context.Context, root, target exec.DAGRunRef) (dagRunRecordItem, error) {
	if root.Name == target.Name && root.ID == target.ID {
		return s.latestRootAttemptForRun(ctx, root.Name, root.ID)
	}
	return s.latestSubAttemptForRun(ctx, root, target)
}

func latestVisibleAttempt(items []dagRunRecordItem) (dagRunRecordItem, bool) {
	var latest dagRunRecordItem
	var ok bool
	for _, item := range items {
		if item.payload.Hidden {
			continue
		}
		if !ok || compareAttemptPayload(item.payload, latest.payload) > 0 {
			latest = item
			ok = true
		}
	}
	return latest, ok
}

func (s *DAGRunStore) rootAttemptItems(ctx context.Context, name string) ([]dagRunRecordItem, error) {
	prefix := dagRunRootPrefix
	if name != "" {
		prefix = dagRunRootPrefix + escapeDAGRunComponent(name) + "/"
	}
	items, err := s.listAttemptItems(ctx, prefix)
	if err != nil {
		return nil, err
	}
	filtered := items[:0]
	for _, item := range items {
		if item.payload.Parent.Zero() {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (s *DAGRunStore) recordsForRootRun(ctx context.Context, name, dagRunID string) ([]dagRunRecordItem, error) {
	items, err := s.listAttemptItems(ctx, rootRunPrefix(name, dagRunID))
	if err != nil {
		return nil, err
	}
	filtered := items[:0]
	for _, item := range items {
		if item.payload.Parent.Zero() {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (s *DAGRunStore) recordsForSubRun(ctx context.Context, root exec.DAGRunRef, subDAGRunID string) ([]dagRunRecordItem, error) {
	return s.listAttemptItems(ctx, subRunPrefix(root, subDAGRunID)+dagRunAttemptPart)
}

func (s *DAGRunStore) recordsForPayload(ctx context.Context, payload dagRunPayload) ([]dagRunRecordItem, error) {
	if payload.Parent.Zero() {
		return s.recordsForRootRun(ctx, payload.Name, payload.DAGRunID)
	}
	return s.recordsForSubRun(ctx, payload.Root, payload.DAGRunID)
}

func (s *DAGRunStore) listAttemptItems(ctx context.Context, prefix string) ([]dagRunRecordItem, error) {
	recs, err := listAllStrict(ctx, s.col, persis.ListQuery{Prefix: prefix})
	if err != nil {
		return nil, err
	}
	items := make([]dagRunRecordItem, 0, len(recs))
	for _, rec := range recs {
		payload, err := decodeDAGRunPayload(rec)
		if err != nil {
			return nil, err
		}
		items = append(items, dagRunRecordItem{record: rec, payload: payload})
	}
	return items, nil
}

func (s *DAGRunStore) payloadByID(ctx context.Context, id string) (dagRunPayload, error) {
	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return dagRunPayload{}, exec.ErrDAGRunIDNotFound
		}
		return dagRunPayload{}, err
	}
	return decodeDAGRunPayload(rec)
}

func (s *DAGRunStore) updatePayload(ctx context.Context, id string, mutate func(*dagRunPayload) error) error {
	return s.withLock(ctx, "record/"+id, func() error {
		rec, err := s.col.Get(ctx, id)
		if err != nil {
			return err
		}
		payload, err := decodeDAGRunPayload(rec)
		if err != nil {
			return err
		}
		if err := mutate(&payload); err != nil {
			return err
		}
		nextData, _, err := persis.Encode(payload)
		if err != nil {
			return err
		}
		return s.col.CompareAndSwap(ctx, id, rec.Data, nextData)
	})
}

func (s *DAGRunStore) putPayload(ctx context.Context, id string, payload dagRunPayload, createdAt, updatedAt time.Time) error {
	data, enc, err := persis.Encode(payload)
	if err != nil {
		return err
	}
	return s.col.Put(ctx, &persis.Record{
		ID:        id,
		Data:      data,
		Encoding:  enc,
		CreatedAt: createdAt.UTC(),
		UpdatedAt: updatedAt.UTC(),
	})
}

func (s *DAGRunStore) ensureAttemptRecordDoesNotExist(ctx context.Context, recordID, attemptID string) error {
	if _, err := s.col.Get(ctx, recordID); err == nil {
		return fmt.Errorf("%w: attempt %s", exec.ErrDAGRunAlreadyExists, attemptID)
	} else if err != nil && !errors.Is(err, persis.ErrNotFound) {
		return err
	}
	return nil
}

func (s *DAGRunStore) ensureRecordDoesNotExist(ctx context.Context, recordID string) error {
	if _, err := s.col.Get(ctx, recordID); err == nil {
		return fmt.Errorf("%w: destination dag-run record %s", exec.ErrDAGRunAlreadyExists, recordID)
	} else if err != nil && !errors.Is(err, persis.ErrNotFound) {
		return err
	}
	return nil
}

func (s *DAGRunStore) deleteRootRun(ctx context.Context, name, dagRunID string) error {
	items, err := s.listAttemptItems(ctx, rootRunPrefix(name, dagRunID))
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := s.col.Delete(ctx, item.record.ID); err != nil && !errors.Is(err, persis.ErrNotFound) {
			return err
		}
	}
	return nil
}

func (s *DAGRunStore) attemptFromRecord(rec *persis.Record, payload dagRunPayload) *DAGRunAttempt {
	return &DAGRunAttempt{
		store:    s,
		id:       payload.AttemptID,
		recordID: rec.ID,
		dag:      cloneDAG(payload.DAG),
	}
}

func decodeDAGRunPayload(rec *persis.Record) (dagRunPayload, error) {
	var payload dagRunPayload
	if err := persis.Decode(rec, &payload); err != nil {
		return dagRunPayload{}, fmt.Errorf("dag-run store: decode %q: %w", rec.ID, err)
	}
	if payload.Version != dagRunStoreVersion {
		return dagRunPayload{}, fmt.Errorf("dag-run store: unsupported record version %d for %q", payload.Version, rec.ID)
	}
	return payload, nil
}

func (s *DAGRunStore) withLock(ctx context.Context, key string, fn func() error) error {
	if col, ok := s.col.(interface {
		WithLock(context.Context, string, func() error) error
	}); ok {
		return col.WithLock(ctx, key, fn)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn()
}
