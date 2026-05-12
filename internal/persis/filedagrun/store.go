// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package filedagrun

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

// Error definitions for common issues
var (
	ErrDAGRunIDEmpty = errors.New("dag-run ID is empty")
)

var _ exec.DAGRunStore = (*Store)(nil)

// Store manages DAG run status files on the local filesystem.
type Store struct {
	baseDir           string                              // Base directory for all status files
	artifactDir       string                              // Trusted root for artifact cleanup
	latestStatusToday bool                                // Whether to only return today's status
	cache             *fileutil.Cache[*exec.DAGRunStatus] // Optional cache for read operations
	maxWorkers        int                                 // Maximum number of parallel workers
	location          *time.Location                      // Timezone location for date calculations
}

// DAGRunStoreOption defines functional options for configuring Store.
type DAGRunStoreOption func(*DAGRunStoreOptions)

// DAGRunStoreOptions holds configuration options for Store.
type DAGRunStoreOptions struct {
	FileCache         *fileutil.Cache[*exec.DAGRunStatus] // Optional cache for status files
	ArtifactDir       string                              // Trusted root for artifact cleanup
	LatestStatusToday bool                                // Whether to only return today's status
	MaxWorkers        int                                 // Maximum number of parallel workers
	Location          *time.Location                      // Timezone location for date calculations
}

// WithHistoryFileCache sets the file cache for Store.
func WithHistoryFileCache(cache *fileutil.Cache[*exec.DAGRunStatus]) DAGRunStoreOption {
	return func(o *DAGRunStoreOptions) {
		o.FileCache = cache
	}
}

// WithArtifactDir sets the trusted root for artifact cleanup operations.
func WithArtifactDir(dir string) DAGRunStoreOption {
	return func(o *DAGRunStoreOptions) {
		o.ArtifactDir = dir
	}
}

// WithLatestStatusToday sets whether to only return today's status.
func WithLatestStatusToday(latestStatusToday bool) DAGRunStoreOption {
	return func(o *DAGRunStoreOptions) {
		o.LatestStatusToday = latestStatusToday
	}
}

// WithLocation sets the timezone location for date calculations.
func WithLocation(location *time.Location) DAGRunStoreOption {
	return func(o *DAGRunStoreOptions) {
		o.Location = location
	}
}

// New creates a new Store instance with the specified options.
func New(baseDir string, opts ...DAGRunStoreOption) exec.DAGRunStore {
	options := &DAGRunStoreOptions{
		LatestStatusToday: true,
		MaxWorkers:        runtime.NumCPU(),
		Location:          time.Local, // Default to local timezone
		ArtifactDir:       filepath.Join(filepath.Dir(filepath.Clean(baseDir)), "artifacts"),
	}

	for _, opt := range opts {
		opt(options)
	}

	return &Store{
		baseDir:           baseDir,
		artifactDir:       options.ArtifactDir,
		latestStatusToday: options.LatestStatusToday,
		cache:             options.FileCache,
		maxWorkers:        options.MaxWorkers,
		location:          options.Location,
	}
}

// ListStatuses retrieves status records based on the provided options.
// It supports filtering by time range, status, and limiting the number of results.
func (store *Store) ListStatuses(ctx context.Context, opts ...exec.ListDAGRunStatusesOption) ([]*exec.DAGRunStatus, error) {
	options, err := prepareListOptions(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare options: %w", err)
	}
	items, _, err := store.listStatusesOrdered(ctx, options, options.Limit, false)
	if err != nil {
		return nil, err
	}
	return items, nil
}

// prepareListOptions processes the provided options and sets default values.
func prepareListOptions(opts []exec.ListDAGRunStatusesOption) (exec.ListDAGRunStatusesOptions, error) {
	var options exec.ListDAGRunStatusesOptions

	// Apply all options
	for _, opt := range opts {
		opt(&options)
	}

	// Set default time range if not specified. Internal reconciliation paths can
	// opt out when they need to scan historical rows explicitly.
	if !options.AllHistory && options.From.IsZero() && options.To.IsZero() {
		options.From = exec.NewUTC(time.Now().Truncate(24 * time.Hour))
	}

	// Enforce a reasonable limit on the number of results
	if !options.Unlimited {
		const maxLimit = 1000
		if options.Limit == 0 || options.Limit > maxLimit {
			options.Limit = maxLimit
		}
	}

	return options, nil
}

// resolveStatus resolves and filters a DAGRunStatus for a single dagRun.
// Uses the index summary for fast filtering when available, falling back to
// reading status.jsonl directly.
func (store *Store) resolveStatus(
	ctx context.Context,
	dagRun *DAGRun,
	labelFilters []core.LabelFilter,
	workspaceFilter *exec.WorkspaceFilter,
	statusesFilter map[core.Status]struct{},
	hasStatusFilter bool,
) *exec.DAGRunStatus {
	// Fast path: use pre-loaded summary for filtering.
	if dagRun.summary != nil {
		if hasStatusFilter {
			if _, ok := statusesFilter[dagRun.summary.Status]; !ok {
				return nil
			}
		}
		if len(labelFilters) > 0 {
			summaryLabels := core.NewLabels(dagRun.summary.Labels)
			if !summaryLabels.MatchesFilters(labelFilters) {
				return nil
			}
		}
		if !workspaceFilter.MatchesLabels(core.NewLabels(dagRun.summary.Labels)) {
			return nil
		}

		// Passed filters — construct status directly from index.
		s := dagRun.summary
		return &exec.DAGRunStatus{
			Parent:               exec.NewDAGRunRef(s.ParentName, s.ParentID),
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
			CreatedAt:            s.CreatedAt,
			AutoRetryCount:       s.AutoRetryCount,
			AutoRetryLimit:       s.AutoRetryLimit,
			AutoRetryInterval:    s.AutoRetryInterval,
			AutoRetryBackoff:     s.AutoRetryBackoff,
			AutoRetryMaxInterval: s.AutoRetryMaxInterval,
			ProcGroup:            s.ProcGroup,
			SuspendFlagName:      s.SuspendFlagName,
			ArchiveDir:           s.ArchiveDir,
		}
	}

	// Standard path: discover latest attempt and read status.
	run, err := dagRun.LatestAttempt(ctx, store.cache)
	if err != nil {
		if !errors.Is(err, exec.ErrNoStatusData) {
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
		statusLabels := core.NewLabels(status.Labels)
		if !statusLabels.MatchesFilters(labelFilters) {
			return nil
		}
	}
	if !workspaceFilter.MatchesLabels(core.NewLabels(status.Labels)) {
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
	dagRun exec.DAGRunRef,
	expectedAttemptID string,
	expectedStatus core.Status,
	mutate func(*exec.DAGRunStatus) error,
) (*exec.DAGRunStatus, bool, error) {
	return store.CompareAndSwapAttemptStatus(ctx, exec.DAGRunAttemptRef{
		DAGRun:    dagRun,
		AttemptID: expectedAttemptID,
	}, expectedStatus, mutate)
}

func (store *Store) CompareAndSwapAttemptStatus(
	ctx context.Context,
	ref exec.DAGRunAttemptRef,
	expectedStatus core.Status,
	mutate func(*exec.DAGRunStatus) error,
) (*exec.DAGRunStatus, bool, error) {
	dagRun := ref.DAGRun
	if dagRun.ID == "" {
		return nil, false, ErrDAGRunIDEmpty
	}
	rootRef := ref.RootOrDAGRun()
	if ref.IsSubDAG() && rootRef.Name == "" {
		return nil, false, fmt.Errorf("missing root dag-run name for sub dag-run %s", dagRun.ID)
	}
	if rootRef.Name == "" {
		rootRef.Name = dagRun.Name
	}
	if rootRef.ID == "" {
		return nil, false, ErrDAGRunIDEmpty
	}

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
	if ref.IsSubDAG() {
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
	if ref.AttemptID != "" && status.AttemptID != ref.AttemptID {
		return status, false, nil
	}
	if ref.AttemptKey != "" && status.AttemptKey != "" && status.AttemptKey != ref.AttemptKey {
		return status, false, nil
	}
	if status.DAGRunID != "" && status.DAGRunID != dagRun.ID {
		return status, false, nil
	}
	if status.Status != expectedStatus {
		return status, false, nil
	}

	if err := attempt.Open(ctx); err != nil {
		return nil, false, fmt.Errorf("open attempt: %w", err)
	}
	defer func() { _ = attempt.Close(ctx) }()

	if err := mutate(status); err != nil {
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

// CreateAttempt creates a new history record for the specified dag-run ID.
// If opts.Root is not nil, it creates a new history record for a sub dag-run.
// If opts.Retry is true, it creates a retry record for the specified dag-run ID.
func (store *Store) CreateAttempt(ctx context.Context, dag *core.DAG, timestamp time.Time, dagRunID string, opts exec.NewDAGRunAttemptOptions) (exec.DAGRunAttempt, error) {
	if dagRunID == "" {
		return nil, ErrDAGRunIDEmpty
	}

	if opts.RootDAGRun != nil {
		return store.newChildRecord(ctx, dag, timestamp, dagRunID, opts)
	}

	dataRoot := NewDataRoot(store.baseDir, dag.Name)
	ts := exec.NewUTC(timestamp)

	lockCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := dataRoot.Lock(lockCtx); err != nil {
		return nil, fmt.Errorf("failed to acquire lock for dag-run %s: %w", dagRunID, err)
	}
	defer func() {
		if err := dataRoot.Unlock(); err != nil {
			logger.Error(ctx, "Failed to unlock dag-run", tag.RunID(dagRunID), tag.Error(err))
		}
	}()

	var run *DAGRun
	if opts.Retry {
		r, err := dataRoot.FindByDAGRunID(ctx, dagRunID)
		if err != nil {
			return nil, fmt.Errorf("failed to find execution: %w", err)
		}
		run = r
	} else {
		// Check if the dag-run already exists
		existingRun, _ := dataRoot.FindByDAGRunID(ctx, dagRunID)
		if existingRun != nil {
			// Error if the dag-run already exists
			return nil, fmt.Errorf("%w: %s", exec.ErrDAGRunAlreadyExists, dagRunID)
		}
		r, err := dataRoot.CreateDAGRun(ts, dagRunID)
		if err != nil {
			return nil, fmt.Errorf("failed to create run: %w", err)
		}
		run = r
	}

	record, err := run.CreateAttempt(ctx, ts, store.cache, opts.AttemptID, WithDAG(dag))
	if err != nil {
		return nil, fmt.Errorf("failed to create record: %w", err)
	}

	return record, nil
}

// newChildRecord creates a new history record for a sub dag-run.
func (b *Store) newChildRecord(ctx context.Context, dag *core.DAG, timestamp time.Time, dagRunID string, opts exec.NewDAGRunAttemptOptions) (exec.DAGRunAttempt, error) {
	dataRoot := NewDataRoot(b.baseDir, opts.RootDAGRun.Name)
	root, err := dataRoot.FindByDAGRunID(ctx, opts.RootDAGRun.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to find root execution: %w", err)
	}

	ts := exec.NewUTC(timestamp)

	var run *DAGRun
	if opts.Retry {
		r, err := root.FindSubDAGRun(ctx, dagRunID)
		if err != nil {
			return nil, fmt.Errorf("failed to find sub dag-run record: %w", err)
		}
		run = r
	} else {
		r, err := root.CreateSubDAGRun(ctx, dagRunID)
		if err != nil {
			return nil, fmt.Errorf("failed to create sub dag-run: %w", err)
		}
		run = r
	}

	record, err := run.CreateAttempt(ctx, ts, b.cache, opts.AttemptID, WithDAG(dag))
	if err != nil {
		logger.Error(ctx, "Failed to create sub dag-run record", tag.Error(err))
		return nil, err
	}

	return record, nil
}

// RecentAttempts returns the most recent history records for the specified DAG name.
func (store *Store) RecentAttempts(ctx context.Context, dagName string, itemLimit int) []exec.DAGRunAttempt {
	if itemLimit <= 0 {
		logger.Warn(ctx, "Invalid itemLimit, using default of 10",
			tag.Limit(itemLimit))
		itemLimit = 10
	}

	// Get the latest matches
	root := NewDataRoot(store.baseDir, dagName)
	items := root.Latest(ctx, itemLimit)

	// Get the latest record for each item
	records := make([]exec.DAGRunAttempt, 0, len(items))
	for _, item := range items {
		record, err := item.LatestAttempt(ctx, store.cache)
		if err != nil {
			logger.Error(ctx, "Failed to get latest record", tag.Error(err))
			continue
		}
		records = append(records, record)
	}

	return records
}

// LatestAttempt returns the most recent history record for the specified DAG name.
// If latestStatusToday is true, it only returns today's status.
func (store *Store) LatestAttempt(ctx context.Context, dagName string) (exec.DAGRunAttempt, error) {
	root := NewDataRoot(store.baseDir, dagName)

	if store.latestStatusToday {
		// Use the configured timezone to calculate "today"
		now := time.Now().In(store.location)
		startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, store.location)
		startOfDayInUTC := exec.NewUTC(startOfDay)

		// Get the latest execution data after the start of the day.
		exec, err := root.LatestAfter(ctx, startOfDayInUTC)
		if err != nil {
			return nil, fmt.Errorf("failed to get latest after: %w", err)
		}

		return exec.LatestAttempt(ctx, store.cache)
	}

	// Get the latest execution data.
	latest := root.Latest(ctx, 1)
	if len(latest) == 0 {
		return nil, exec.ErrNoStatusData
	}
	return latest[0].LatestAttempt(ctx, store.cache)
}

// FindAttempt finds a history record by dag-run ID.
func (store *Store) FindAttempt(ctx context.Context, ref exec.DAGRunRef) (exec.DAGRunAttempt, error) {
	if ref.ID == "" {
		return nil, ErrDAGRunIDEmpty
	}

	root := NewDataRoot(store.baseDir, ref.Name)
	run, err := root.FindByDAGRunID(ctx, ref.ID)
	if err != nil {
		return nil, err
	}

	return run.LatestAttempt(ctx, store.cache)
}

// FindSubAttempt finds a sub dag-run by its ID.
// It returns the latest record for the specified sub dag-run ID.
func (store *Store) FindSubAttempt(ctx context.Context, ref exec.DAGRunRef, subDAGRunID string) (exec.DAGRunAttempt, error) {
	if ref.ID == "" {
		return nil, ErrDAGRunIDEmpty
	}

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

// CreateSubAttempt creates a new sub dag-run attempt under the root dag-run.
// This is used for distributed sub-DAG execution where the coordinator needs
// to create the attempt directory before the worker reports status.
func (store *Store) CreateSubAttempt(ctx context.Context, rootRef exec.DAGRunRef, subDAGRunID string) (exec.DAGRunAttempt, error) {
	if rootRef.ID == "" {
		return nil, ErrDAGRunIDEmpty
	}

	root := NewDataRoot(store.baseDir, rootRef.Name)

	// Acquire lock to prevent concurrent creation conflicts
	lockCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := root.Lock(lockCtx); err != nil {
		return nil, fmt.Errorf("failed to acquire lock for sub dag-run %s: %w", subDAGRunID, err)
	}
	defer func() {
		if err := root.Unlock(); err != nil {
			logger.Error(ctx, "Failed to unlock sub dag-run", tag.RunID(subDAGRunID), tag.Error(err))
		}
	}()

	dagRun, err := root.FindByDAGRunID(ctx, rootRef.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to find root dag-run: %w", err)
	}

	// Create the sub-DAG run directory
	subDAGRun, err := dagRun.CreateSubDAGRun(ctx, subDAGRunID)
	if err != nil {
		return nil, fmt.Errorf("failed to create sub dag-run directory: %w", err)
	}

	// Create an attempt within the sub-DAG run (no preset attemptID)
	return subDAGRun.CreateAttempt(ctx, exec.NewUTC(time.Now()), store.cache, "")
}

// RemoveOldDAGRuns removes old history records by retention days, or by run count when configured by option.
// Without run-count options, it only removes records older than the specified retention days.
// If retentionDays is negative, no files will be removed.
// If retentionDays is zero, all files will be removed.
// If retentionDays is positive, only files older than the specified number of days will be removed.
// Returns a list of file paths that were removed (or would be removed in dry-run mode).
func (store *Store) RemoveOldDAGRuns(ctx context.Context, dagName string, retentionDays int, opts ...exec.RemoveOldDAGRunsOption) ([]string, error) {
	var options exec.RemoveOldDAGRunsOptions
	for _, opt := range opts {
		opt(&options)
	}

	if options.RetentionRuns != nil {
		retentionRuns := *options.RetentionRuns
		if retentionRuns <= 0 {
			logger.Warn(ctx, "Non-positive retentionRuns, no files will be removed",
				slog.Int("retention-runs", retentionRuns),
			)
			return nil, nil
		}
		root := NewDataRootWithArtifactDir(store.baseDir, dagName, store.artifactDir)
		return root.RemoveOldByRuns(ctx, retentionRuns, options.DryRun)
	}

	if retentionDays < 0 {
		logger.Warn(ctx, "Negative retentionDays, no files will be removed",
			slog.Int("retention-days", retentionDays),
		)
		return nil, nil
	}

	root := NewDataRootWithArtifactDir(store.baseDir, dagName, store.artifactDir)
	return root.RemoveOld(ctx, retentionDays, options.DryRun)
}

// RemoveDAGRun implements models.DAGRunStore.
func (store *Store) RemoveDAGRun(ctx context.Context, dagRun exec.DAGRunRef, opts ...exec.RemoveDAGRunOption) error {
	if dagRun.ID == "" {
		return ErrDAGRunIDEmpty
	}

	var options exec.RemoveDAGRunOptions
	for _, opt := range opts {
		opt(&options)
	}

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

	if options.RejectActive {
		attempt, err := run.LatestAttempt(ctx, store.cache)
		if err != nil {
			return fmt.Errorf("failed to find latest attempt for dag-run %s: %w", dagRun.ID, err)
		}
		status, err := attempt.ReadStatus(ctx)
		if err != nil {
			return fmt.Errorf("failed to read dag-run %s status: %w", dagRun.ID, err)
		}
		if status == nil {
			return fmt.Errorf("failed to read dag-run %s status: %w", dagRun.ID, exec.ErrNoStatusData)
		}
		if status.Status.IsActive() {
			return fmt.Errorf("%w: %s", exec.ErrDAGRunActive, status.Status.String())
		}
	}

	if err := run.Remove(ctx); err != nil {
		return fmt.Errorf("failed to remove dag-run %s: %w", dagRun.ID, err)
	}

	return nil
}

// RenameDAGRuns renames all history records for the specified DAG name.
func (store *Store) RenameDAGRuns(ctx context.Context, oldNameOrPath, newNameOrPath string) error {
	root := NewDataRoot(store.baseDir, oldNameOrPath)
	newRoot := NewDataRoot(store.baseDir, newNameOrPath)
	return root.Rename(ctx, newRoot)
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
