// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

var _ persis.DAGRunStore = (*Store)(nil)

// Store manages DAG run status files on the local filesystem.
type Store struct {
	baseDir         string
	artifactDir     string
	cache           *fileutil.Cache[*ir.DAGRunStatus]
	retryCandidates retryCandidateCache
}

// StoreOption configures filesystem DAG-run storage.
type StoreOption func(*options)

type options struct {
	fileCache   *fileutil.Cache[*ir.DAGRunStatus]
	artifactDir string
}

// WithHistoryFileCache sets the file cache for Store.
func WithHistoryFileCache(cache *fileutil.Cache[*ir.DAGRunStatus]) StoreOption {
	return func(o *options) {
		o.fileCache = cache
	}
}

// WithArtifactDir sets the trusted root for artifact cleanup operations.
func WithArtifactDir(dir string) StoreOption {
	return func(o *options) {
		o.artifactDir = dir
	}
}

func newOptions(baseDir string, opts []StoreOption) options {
	cfg := options{
		artifactDir: filepath.Join(filepath.Dir(filepath.Clean(baseDir)), "artifacts"),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return cfg
}

// NewStore creates filesystem DAG-run storage.
func NewStore(baseDir string, opts ...StoreOption) *Store {
	cfg := newOptions(baseDir, opts)
	return &Store{
		baseDir:     baseDir,
		artifactDir: cfg.artifactDir,
		cache:       cfg.fileCache,
	}
}

// resolveStatus resolves and filters a DAGRunStatus for a single dagRun.
// Uses the index summary for fast filtering when available, falling back to
// reading status.jsonl directly.
func (store *Store) resolveStatus(
	ctx context.Context,
	dagRun *DAGRun,
	labelFilters []ir.LabelFilter,
	workspaceFilter *workspace.WorkspaceFilter,
	statusesFilter map[ir.Status]struct{},
	hasStatusFilter bool,
) *ir.DAGRunStatus {
	// Fast path: use pre-loaded summary for filtering.
	if dagRun.summary != nil {
		if hasStatusFilter {
			if _, ok := statusesFilter[dagRun.summary.Status]; !ok {
				return nil
			}
		}
		if len(labelFilters) > 0 {
			summaryLabels := ir.NewLabels(dagRun.summary.Labels)
			if !summaryLabels.MatchesFilters(labelFilters) {
				return nil
			}
		}
		if !workspaceFilter.MatchesLabels(ir.NewLabels(dagRun.summary.Labels)) {
			return nil
		}

		// Passed filters — construct status directly from index.
		s := dagRun.summary
		return &ir.DAGRunStatus{
			Parent:               ir.NewDAGRunRef(s.ParentName, s.ParentID),
			Name:                 s.Name,
			DAGRunID:             s.DagRunID,
			AttemptID:            s.AttemptID,
			Status:               s.Status,
			Labels:               s.Labels,
			StartedAt:            formatUnixToRFC3339(s.StartedAtUnix),
			FinishedAt:           formatUnixToRFC3339(s.FinishedAtUnix),
			WorkerID:             s.WorkerID,
			LeaseAt:              s.LeaseAt,
			Params:               s.Params,
			QueuedAt:             s.QueuedAt,
			ScheduleTime:         s.ScheduleTime,
			TriggerType:          s.TriggerType,
			TriggerActor:         s.TriggerActor,
			CreatedAt:            s.CreatedAt,
			AutoRetryCount:       s.AutoRetryCount,
			AutoRetryLimit:       s.AutoRetryLimit,
			AutoRetryInterval:    s.AutoRetryInterval,
			AutoRetryBackoff:     s.AutoRetryBackoff,
			AutoRetryMaxInterval: s.AutoRetryMaxInterval,
			ProcGroup:            s.ProcGroup,
			DefinitionID:         s.DefinitionID,
			ArchiveDir:           s.ArchiveDir,
		}
	}

	// Standard path: discover latest attempt and read status.
	run, err := dagRun.LatestAttempt(ctx, store.cache)
	if err != nil {
		if !errors.Is(err, dagrun.ErrNoStatusData) {
			logger.Error(ctx, "Failed to get latest run", tag.Error(err))
		}
		return nil
	}

	status, err := run.ReadStatus(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to read status", tag.Error(err))
		return nil
	}

	if len(labelFilters) > 0 {
		statusLabels := ir.NewLabels(status.Labels)
		if !statusLabels.MatchesFilters(labelFilters) {
			return nil
		}
	}
	if !workspaceFilter.MatchesLabels(ir.NewLabels(status.Labels)) {
		return nil
	}

	if hasStatusFilter {
		if _, ok := statusesFilter[status.Status]; !ok {
			return nil
		}
	}

	return status
}

func (store *Store) CompareAndSwapLatestAttemptStatus(
	ctx context.Context,
	req persis.DAGRunCompareAndSwapStatusRequest,
) (*ir.DAGRunStatus, bool, error) {
	dagRun := req.DAGRun
	rootRef := req.RootDAGRun
	isSubDAG := rootRef.ID != dagRun.ID || rootRef.Name != dagRun.Name

	root := NewDataRoot(store.baseDir, rootRef.Name)
	lockCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := root.Lock(lockCtx); err != nil {
		return nil, false, fmt.Errorf("failed to acquire lock for dag-run %s: %w", dagRun.ID, err)
	}
	defer func() {
		if err := root.Unlock(); err != nil {
			logger.Error(ctx, "Failed to unlock dag-run", tag.RunID(dagRun.ID), tag.Error(err))
		}
	}()

	run, err := root.FindByDAGRunID(ctx, rootRef.ID)
	if err != nil {
		return nil, false, err
	}
	if isSubDAG {
		run, err = run.FindSubDAGRun(ctx, dagRun.ID)
		if err != nil {
			return nil, false, err
		}
	}

	attempt, err := run.LatestAttempt(ctx, store.cache)
	if err != nil {
		return nil, false, err
	}

	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, false, err
	}
	if req.ExpectedAttemptID != "" && status.AttemptID != req.ExpectedAttemptID {
		return status, false, nil
	}
	if req.ExpectedAttemptKey != "" && status.AttemptKey != req.ExpectedAttemptKey {
		return status, false, nil
	}
	if status.DAGRunID != "" && status.DAGRunID != dagRun.ID {
		return status, false, nil
	}
	if status.Status != req.ExpectedStatus {
		return status, false, nil
	}

	if err := attempt.Open(ctx); err != nil {
		return nil, false, fmt.Errorf("open attempt: %w", err)
	}
	defer func() { _ = attempt.Close(ctx) }()

	if err := req.Mutate(status); err != nil {
		return nil, false, err
	}
	if err := attempt.Write(ctx, *status); err != nil {
		return nil, false, err
	}
	return status, true, nil
}

func formatUnixToRFC3339(unix int64) string {
	if unix == 0 {
		return ""
	}
	return time.Unix(unix, 0).UTC().Format(time.RFC3339)
}

// CreateAttempt creates an attempt within a root or child DAG run.
func (store *Store) CreateAttempt(ctx context.Context, req persis.DAGRunCreateAttemptRequest) (dagrun.Attempt, error) {
	if !req.RootDAGRun.Zero() {
		return store.newChildAttempt(ctx, req)
	}

	dataRoot := NewDataRoot(store.baseDir, req.DAG.Name)
	ts := persis.NewUTC(req.Timestamp)

	lockCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := dataRoot.Lock(lockCtx); err != nil {
		return nil, fmt.Errorf("failed to acquire lock for dag-run %s: %w", req.DAGRunID, err)
	}
	defer func() {
		if err := dataRoot.Unlock(); err != nil {
			logger.Error(ctx, "Failed to unlock dag-run", tag.RunID(req.DAGRunID), tag.Error(err))
		}
	}()

	var run *DAGRun
	if req.Retry {
		r, err := dataRoot.FindByDAGRunID(ctx, req.DAGRunID)
		if err != nil {
			return nil, fmt.Errorf("failed to find execution: %w", err)
		}
		run = r
	} else {
		// Check if the dag-run already exists
		existingRun, _ := dataRoot.FindByDAGRunID(ctx, req.DAGRunID)
		if existingRun != nil {
			// Error if the dag-run already exists
			return nil, fmt.Errorf("%w: %s", dagrun.ErrDAGRunAlreadyExists, req.DAGRunID)
		}
		r, err := dataRoot.CreateDAGRun(ts, req.DAGRunID)
		if err != nil {
			return nil, fmt.Errorf("failed to create run: %w", err)
		}
		run = r
	}

	attempt, err := run.CreateAttempt(ctx, ts, store.cache, req.AttemptID)
	if err != nil {
		return nil, fmt.Errorf("failed to create attempt: %w", err)
	}
	attempt.SetDAG(req.DAG)

	return attempt, nil
}

func (store *Store) newChildAttempt(ctx context.Context, req persis.DAGRunCreateAttemptRequest) (dagrun.Attempt, error) {
	dataRoot := NewDataRoot(store.baseDir, req.RootDAGRun.Name)
	lockCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := dataRoot.Lock(lockCtx); err != nil {
		return nil, fmt.Errorf("failed to acquire lock for sub dag-run %s: %w", req.DAGRunID, err)
	}
	defer func() {
		if err := dataRoot.Unlock(); err != nil {
			logger.Error(ctx, "Failed to unlock sub dag-run", tag.RunID(req.DAGRunID), tag.Error(err))
		}
	}()

	root, err := dataRoot.FindByDAGRunID(ctx, req.RootDAGRun.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to find root execution: %w", err)
	}

	ts := persis.NewUTC(req.Timestamp)

	var run *DAGRun
	if req.Retry {
		r, err := root.FindSubDAGRun(ctx, req.DAGRunID)
		if err != nil {
			return nil, fmt.Errorf("failed to find sub dag-run attempt: %w", err)
		}
		run = r
	} else {
		r, err := root.CreateSubDAGRun(ctx, req.DAGRunID)
		if err != nil {
			return nil, fmt.Errorf("failed to create sub dag-run: %w", err)
		}
		run = r
	}

	attempt, err := run.CreateAttempt(ctx, ts, store.cache, req.AttemptID)
	if err != nil {
		logger.Error(ctx, "Failed to create sub dag-run attempt", tag.Error(err))
		return nil, err
	}
	attempt.SetDAG(req.DAG)

	return attempt, nil
}

// RecentStatuses returns the newest readable status for recent DAG runs.
func (store *Store) RecentStatuses(ctx context.Context, dagName string, itemLimit int) ([]ir.DAGRunStatus, error) {
	root := NewDataRoot(store.baseDir, dagName)
	items, err := root.listRecentDAGRuns(ctx, itemLimit)
	if err != nil {
		return nil, err
	}

	statuses := make([]ir.DAGRunStatus, 0, len(items))
	for _, item := range items {
		attempt, err := item.LatestAttempt(ctx, store.cache)
		if err != nil {
			logger.Error(ctx, "Failed to get latest attempt", tag.Error(err))
			continue
		}
		status, err := attempt.ReadStatus(ctx)
		if err != nil {
			logger.Error(ctx, "Failed to read recent DAG-run status", tag.Error(err))
			continue
		}
		statuses = append(statuses, *status)
	}

	return statuses, nil
}

// LatestAttempt returns the newest visible attempt matching the query.
func (store *Store) LatestAttempt(ctx context.Context, query persis.DAGRunLatestAttemptQuery) (dagrun.Attempt, error) {
	root := NewDataRoot(store.baseDir, query.Name)

	if !query.NotBefore.IsZero() {
		if attempt, err := root.latestAttemptFromPointer(ctx, store.cache, query.NotBefore); err == nil {
			return attempt, nil
		}

		exec, err := root.LatestAfter(ctx, query.NotBefore)
		if err != nil {
			return nil, fmt.Errorf("failed to get latest after: %w", err)
		}

		attempt, err := exec.LatestAttempt(ctx, store.cache)
		if err == nil {
			if markerErr := updateLatestAttemptPointer(ctx, attempt.file); markerErr != nil {
				logger.Debug(ctx, "Failed to refresh DAG-run latest attempt pointer", tag.Error(markerErr))
			}
		}
		return attempt, err
	}

	if attempt, err := root.latestAttemptFromPointer(ctx, store.cache, persis.TimeInUTC{}); err == nil {
		return attempt, nil
	}

	// Get the latest execution data.
	latest := root.Latest(ctx, 1)
	if len(latest) == 0 {
		return nil, dagrun.ErrNoStatusData
	}
	attempt, err := latest[0].LatestAttempt(ctx, store.cache)
	if err == nil {
		if markerErr := updateLatestAttemptPointer(ctx, attempt.file); markerErr != nil {
			logger.Debug(ctx, "Failed to refresh DAG-run latest attempt pointer", tag.Error(markerErr))
		}
	}
	return attempt, err
}

// FindAttempt finds the latest attempt by DAG-run ID.
func (store *Store) FindAttempt(ctx context.Context, ref ir.DAGRunRef) (dagrun.Attempt, error) {
	root := NewDataRoot(store.baseDir, ref.Name)
	run, err := root.FindByDAGRunID(ctx, ref.ID)
	if err != nil {
		return nil, err
	}

	return run.LatestAttempt(ctx, store.cache)
}

// FindSubAttempt finds a sub dag-run by its ID.
// It returns the latest attempt for the specified sub DAG-run ID.
func (store *Store) FindSubAttempt(ctx context.Context, ref ir.DAGRunRef, subDAGRunID string) (dagrun.Attempt, error) {
	root := NewDataRoot(store.baseDir, ref.Name)
	dagRun, err := root.FindByDAGRunID(ctx, ref.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to find execution: %w", err)
	}

	subDAGRun, err := dagRun.FindSubDAGRun(ctx, subDAGRunID)
	if err != nil {
		return nil, fmt.Errorf("failed to find sub dag-run: %w", err)
	}
	return subDAGRun.LatestAttempt(ctx, store.cache)
}

// RemoveOldDAGRuns removes final runs outside a normalized retention policy.
func (store *Store) RemoveOldDAGRuns(ctx context.Context, req persis.DAGRunRetentionRequest) ([]ir.DAGRunRef, error) {
	root := NewDataRootWithArtifactDir(store.baseDir, req.Name, store.artifactDir)
	var (
		ids []string
		err error
	)
	if req.KeepRuns > 0 {
		ids, err = root.RemoveOldByRuns(ctx, req.KeepRuns, req.DryRun)
	} else {
		ids, err = root.removeOldBefore(ctx, req.OlderThan, req.DryRun)
	}
	refs := make([]ir.DAGRunRef, 0, len(ids))
	for _, id := range ids {
		refs = append(refs, ir.NewDAGRunRef(req.Name, id))
	}
	return refs, err
}

// RemoveDAGRun removes a DAG run and all of its attempts.
func (store *Store) RemoveDAGRun(ctx context.Context, req persis.DAGRunRemoveRequest) error {
	dagRun := req.DAGRun
	root := NewDataRootWithArtifactDir(store.baseDir, dagRun.Name, store.artifactDir)
	if err := root.Lock(ctx); err != nil {
		return fmt.Errorf("failed to acquire lock for dag-run %s: %w", dagRun.ID, err)
	}

	defer func() {
		if err := root.Unlock(); err != nil {
			logger.Error(ctx, "Failed to unlock dag-run", tag.RunID(dagRun.ID), tag.Error(err))
		}
	}()

	run, err := root.FindByDAGRunID(ctx, dagRun.ID)
	if err != nil {
		return fmt.Errorf("failed to find dag-run %s: %w", dagRun.ID, err)
	}

	if req.RejectActive {
		attempt, err := run.LatestAttempt(ctx, store.cache)
		if err != nil {
			return fmt.Errorf("failed to find latest attempt for dag-run %s: %w", dagRun.ID, err)
		}
		status, err := attempt.ReadStatus(ctx)
		if err != nil {
			return fmt.Errorf("failed to read dag-run %s status: %w", dagRun.ID, err)
		}
		if status == nil {
			return fmt.Errorf("failed to read dag-run %s status: %w", dagRun.ID, dagrun.ErrNoStatusData)
		}
		if status.Status.IsActive() {
			return fmt.Errorf("%w: %s", dagrun.ErrDAGRunActive, status.Status.String())
		}
	}

	if err := root.removeDAGRun(ctx, run, false); err != nil {
		return fmt.Errorf("failed to remove dag-run %s: %w", dagRun.ID, err)
	}

	return nil
}

// listRoot lists all root directories in the base directory.
func (store *Store) listRoot(_ context.Context, include string) ([]DataRoot, error) {
	rootDirs, err := listDirsSorted(store.baseDir, false, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list root directories: %w", err)
	}

	var roots []DataRoot
	for _, dir := range rootDirs {
		if include != "" && dir != include {
			continue
		}
		if fileutil.IsDir(filepath.Join(store.baseDir, dir)) {
			root := NewDataRoot(store.baseDir, dir)
			roots = append(roots, root)
		}
	}

	return roots, nil
}
