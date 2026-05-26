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

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis"
	"github.com/dagucloud/dagu/internal/persis/filedagrun"
)

const (
	dagRunStoreVersion = 1

	// DAG-run records are stored as:
	// runs/<dagName>/<dagRunID>/attempts/<attemptID>
	// runs/<dagName>/<dagRunID>/children/<subDAGRunID>/attempts/<attemptID>
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

// WithDAGRunArtifactDir sets the trusted artifact root for file-backed DAG-run
// stores. This option has no effect when the collection does not expose a file
// root.
func WithDAGRunArtifactDir(dir string) DAGRunStoreOption {
	return func(s *DAGRunStore) {
		s.artifactDir = dir
	}
}

// WithDAGRunHistoryFileCache sets the status-file cache for file-backed DAG-run
// stores used by long-running processes. This option has no effect when the
// collection does not expose a file root.
func WithDAGRunHistoryFileCache(cache *fileutil.Cache[*exec.DAGRunStatus]) DAGRunStoreOption {
	return func(s *DAGRunStore) {
		s.historyFileCache = cache
	}
}

// DAGRunStore implements [exec.DAGRunStore]. When constructed with the file
// backend collection it preserves the existing filedagrun layout; other
// backends use collection records for control-plane metadata only.
type DAGRunStore struct {
	col              persis.Collection
	fileStore        exec.DAGRunStore
	artifactDir      string
	historyFileCache *fileutil.Cache[*exec.DAGRunStatus]

	latestStatusToday bool
	location          *time.Location
	mu                sync.Mutex // guards fallbackLocks
	fallbackLocks     map[string]*dagRunFallbackLock
}

var _ exec.DAGRunStore = (*DAGRunStore)(nil)

type dagRunFallbackLock struct {
	mu   sync.Mutex
	refs int
}

type dagRunPayload struct {
	Version   int    `json:"version"`
	Name      string `json:"name"`
	DAGRunID  string `json:"dagRunId"`
	AttemptID string `json:"attemptId"`
	// Root and Parent rely on Go 1.26 encoding/json omitzero support so
	// zero DAGRunRef structs are omitted; omitempty would still encode them.
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
		fallbackLocks:     make(map[string]*dagRunFallbackLock),
	}
	for _, opt := range opts {
		opt(s)
	}
	if rooted, ok := col.(interface{ RootDir() string }); ok {
		fileOpts := []filedagrun.DAGRunStoreOption{
			filedagrun.WithLatestStatusToday(s.latestStatusToday),
			filedagrun.WithLocation(s.location),
		}
		if lockRooted, ok := col.(interface{ LockRootDir() string }); ok {
			if lockRoot := lockRooted.LockRootDir(); lockRoot != "" {
				fileOpts = append(fileOpts, filedagrun.WithLockRoot(lockRoot))
			}
		}
		if s.artifactDir != "" {
			fileOpts = append(fileOpts, filedagrun.WithArtifactDir(s.artifactDir))
		}
		if s.historyFileCache != nil {
			fileOpts = append(fileOpts, filedagrun.WithHistoryFileCache(s.historyFileCache))
		}
		s.fileStore = filedagrun.New(rooted.RootDir(), fileOpts...)
	}
	return s
}

func (s *DAGRunStore) CreateAttempt(ctx context.Context, dag *core.DAG, ts time.Time, dagRunID string, opts exec.NewDAGRunAttemptOptions) (exec.DAGRunAttempt, error) {
	if s.fileStore != nil {
		return s.fileStore.CreateAttempt(ctx, dag, ts, dagRunID, opts)
	}
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
	if s.fileStore != nil {
		return s.fileStore.RecentAttempts(ctx, name, itemLimit)
	}
	if name == "" {
		return nil
	}
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
	if s.fileStore != nil {
		return s.fileStore.LatestAttempt(ctx, dagName)
	}
	if dagName == "" {
		return nil, fmt.Errorf("dag-run store: DAG name is required")
	}
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
	if s.fileStore != nil {
		return s.fileStore.ListStatuses(ctx, opts...)
	}
	options, err := prepareDAGRunListOptions(s.location, opts)
	if err != nil {
		return nil, err
	}
	statuses, _, err := s.listStatuses(ctx, options, options.Limit, false)
	return statuses, err
}

func (s *DAGRunStore) ListStatusesPage(ctx context.Context, opts ...exec.ListDAGRunStatusesOption) (exec.DAGRunStatusPage, error) {
	if s.fileStore != nil {
		return s.fileStore.ListStatusesPage(ctx, opts...)
	}
	options, err := prepareDAGRunListOptions(s.location, opts)
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
	if s.fileStore != nil {
		return s.fileStore.CompareAndSwapLatestAttemptStatus(ctx, dagRun, expectedAttemptID, expectedStatus, mutate, opts...)
	}
	if dagRun.ID == "" {
		return nil, false, ErrDAGRunIDEmpty
	}
	cfg := exec.NewCompareAndSwapStatusOptions(opts...)
	rootRef := cfg.RootDAGRun
	if rootRef.Zero() {
		if dagRun.Name == "" {
			return nil, false, fmt.Errorf("dag-run store: DAG name is required")
		}
		rootRef = dagRun
	} else if rootRef.Name == "" {
		return nil, false, fmt.Errorf("dag-run store: root DAG name is required")
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
	if s.fileStore != nil {
		return s.fileStore.FindAttempt(ctx, ref)
	}
	if ref.ID == "" {
		return nil, ErrDAGRunIDEmpty
	}
	if ref.Name == "" {
		return nil, fmt.Errorf("dag-run store: DAG name is required")
	}
	item, err := s.latestRootAttemptForRun(ctx, ref.Name, ref.ID)
	if err != nil {
		return nil, err
	}
	return s.attemptFromRecord(item.record, item.payload), nil
}

func (s *DAGRunStore) FindSubAttempt(ctx context.Context, ref exec.DAGRunRef, subDAGRunID string) (exec.DAGRunAttempt, error) {
	if s.fileStore != nil {
		return s.fileStore.FindSubAttempt(ctx, ref, subDAGRunID)
	}
	if ref.ID == "" {
		return nil, ErrDAGRunIDEmpty
	}
	if ref.Name == "" {
		return nil, fmt.Errorf("dag-run store: root DAG name is required")
	}
	if subDAGRunID == "" {
		return nil, ErrDAGRunIDEmpty
	}
	item, err := s.latestSubAttemptForRun(ctx, ref, exec.NewDAGRunRef("", subDAGRunID))
	if err != nil {
		return nil, fmt.Errorf("failed to find sub dag-run: %w", err)
	}
	return s.attemptFromRecord(item.record, item.payload), nil
}

func (s *DAGRunStore) CreateSubAttempt(ctx context.Context, rootRef exec.DAGRunRef, subDAGRunID string) (exec.DAGRunAttempt, error) {
	if s.fileStore != nil {
		return s.fileStore.CreateSubAttempt(ctx, rootRef, subDAGRunID)
	}
	if rootRef.ID == "" {
		return nil, ErrDAGRunIDEmpty
	}
	if rootRef.Name == "" {
		return nil, fmt.Errorf("dag-run store: root DAG name is required")
	}
	return s.createSubAttempt(ctx, nil, time.Now().UTC(), subDAGRunID, exec.NewDAGRunAttemptOptions{RootDAGRun: &rootRef})
}

// RemoveOldDAGRuns removes old history records by retention days, or by run
// count when WithRetentionRuns is configured. Run-count retention matches the
// file-backed store and takes precedence over retentionDays.
func (s *DAGRunStore) RemoveOldDAGRuns(ctx context.Context, name string, retentionDays int, opts ...exec.RemoveOldDAGRunsOption) ([]string, error) {
	if s.fileStore != nil {
		return s.fileStore.RemoveOldDAGRuns(ctx, name, retentionDays, opts...)
	}
	var options exec.RemoveOldDAGRunsOptions
	for _, opt := range opts {
		opt(&options)
	}
	if retentionDays < 0 && options.RetentionRuns == nil {
		return nil, nil
	}
	if options.RetentionRuns != nil && *options.RetentionRuns < 0 {
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
			keep[dagRunKey(item.payload)] = struct{}{}
		}
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
	var removed []string
	seen := make(map[string]struct{})
	for _, item := range attempts {
		key := dagRunKey(item.payload)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if _, ok := keep[key]; ok {
			continue
		}
		if item.payload.Status == nil {
			continue
		}
		if item.payload.Status.Status.IsActive() {
			continue
		}
		if options.RetentionRuns == nil && retentionDays > 0 && !dagRunAttemptActivity(item).Before(cutoff) {
			continue
		}
		removed = append(removed, item.payload.DAGRunID)
		if options.DryRun {
			continue
		}
		if err := s.deleteRootRun(ctx, item.payload.Name, item.payload.DAGRunID); err != nil {
			return nil, err
		}
	}
	return removed, nil
}

func (s *DAGRunStore) RenameDAGRuns(ctx context.Context, oldName, newName string) error {
	if s.fileStore != nil {
		return s.fileStore.RenameDAGRuns(ctx, oldName, newName)
	}
	if oldName == "" || newName == "" {
		return fmt.Errorf("dag-run store: old and new names are required")
	}
	if oldName == newName {
		return nil
	}
	return s.withLocks(ctx, []string{
		"rename/" + escapeDAGRunComponent(oldName),
		"rename/" + escapeDAGRunComponent(newName),
	}, func() error {
		items, err := s.listAttemptItems(ctx, dagRunRootPrefix+escapeDAGRunComponent(oldName)+"/")
		if err != nil {
			return err
		}
		ops := make([]dagRunRenameOp, 0, len(items))
		for _, item := range items {
			next, newID := renameDAGRunPayload(item.payload, oldName, newName)
			ops = append(ops, dagRunRenameOp{
				oldID:      item.record.ID,
				newID:      newID,
				oldPayload: item.payload,
				payload:    next,
				createdAt:  item.record.CreatedAt,
				updatedAt:  time.Now().UTC(),
			})
		}
		return s.withLocks(ctx, dagRunRenameLockKeys(ops), func() error {
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
	})
}

func (s *DAGRunStore) RemoveDAGRun(ctx context.Context, dagRun exec.DAGRunRef, opts ...exec.RemoveDAGRunOption) error {
	if s.fileStore != nil {
		return s.fileStore.RemoveDAGRun(ctx, dagRun, opts...)
	}
	if dagRun.ID == "" {
		return ErrDAGRunIDEmpty
	}
	if dagRun.Name == "" {
		return fmt.Errorf("dag-run store: DAG name is required")
	}
	var options exec.RemoveDAGRunOptions
	for _, opt := range opts {
		opt(&options)
	}
	return s.withLock(ctx, dagRunLockKey(dagRun.Name, dagRun.ID), func() error {
		records, err := s.recordsForRootRun(ctx, dagRun.Name, dagRun.ID)
		if err != nil {
			return err
		}
		if len(records) == 0 {
			return exec.ErrDAGRunIDNotFound
		}
		if options.RejectActive {
			item, ok := latestVisibleAttempt(records)
			if !ok {
				return fmt.Errorf("failed to find latest attempt for dag-run %s: %w", dagRun.ID, exec.ErrNoStatusData)
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
	if rootRef.Name == "" {
		return nil, fmt.Errorf("dag-run store: root DAG name is required")
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
	oldID      string
	newID      string
	oldPayload dagRunPayload
	payload    dagRunPayload
	createdAt  time.Time
	updatedAt  time.Time
}

func dagRunKey(payload dagRunPayload) string {
	return payload.Name + "\x00" + payload.DAGRunID
}

func dagRunRenameLockKeys(ops []dagRunRenameOp) []string {
	keys := make([]string, 0, len(ops)*2)
	for _, op := range ops {
		keys = append(keys, dagRunPayloadLockKey(op.oldPayload))
		keys = append(keys, dagRunPayloadLockKey(op.payload))
	}
	return keys
}

func dagRunPayloadLockKey(payload dagRunPayload) string {
	if payload.Parent.Zero() {
		return dagRunLockKey(payload.Name, payload.DAGRunID)
	}
	return dagRunLockKey(payload.Root.Name, payload.Root.ID)
}

func normalizeDAGRunLockKeys(keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	out := sorted[:0]
	for _, key := range sorted {
		if key == "" {
			continue
		}
		if len(out) > 0 && out[len(out)-1] == key {
			continue
		}
		out = append(out, key)
	}
	return out
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
		key := dagRunKey(item.payload)
		current, ok := latestByRun[key]
		if !ok || compareAttemptPayload(item.payload, current.payload) > 0 {
			latestByRun[key] = item
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
	rec, err := s.col.Get(ctx, id)
	if err != nil {
		return err
	}
	payload, err := decodeDAGRunPayload(rec)
	if err != nil {
		return err
	}
	return s.withLock(ctx, dagRunPayloadLockKey(payload), func() error {
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
	unlock := s.lockFallbackKey(key)
	defer unlock()
	return fn()
}

func (s *DAGRunStore) withLocks(ctx context.Context, keys []string, fn func() error) error {
	keys = normalizeDAGRunLockKeys(keys)
	var lockNext func(int) error
	lockNext = func(i int) error {
		if i >= len(keys) {
			return fn()
		}
		return s.withLock(ctx, keys[i], func() error {
			return lockNext(i + 1)
		})
	}
	return lockNext(0)
}

func (s *DAGRunStore) lockFallbackKey(key string) func() {
	s.mu.Lock()
	lock := s.fallbackLocks[key]
	if lock == nil {
		lock = &dagRunFallbackLock{}
		s.fallbackLocks[key] = lock
	}
	lock.refs++
	s.mu.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()

		s.mu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(s.fallbackLocks, key)
		}
		s.mu.Unlock()
	}
}
