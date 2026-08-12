// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// RepositoryOptions configures application-level DAG-run behavior.
type RepositoryOptions struct {
	LatestStatusToday bool
	Location          *time.Location
	Now               func() time.Time
}

// Repository provides application-level access to persisted DAG runs.
type Repository struct {
	store             Store
	dagRunWorkspaces  DAGRunWorkspaceStore
	latestStatusToday bool
	location          *time.Location
	now               func() time.Time
}

// NewRepository creates a repository backed by store.
func NewRepository(store Store, dagRunWorkspaces DAGRunWorkspaceStore, options RepositoryOptions) *Repository {
	location := options.Location
	if location == nil {
		location = time.Local
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	if dagRunWorkspaces == nil {
		dagRunWorkspaces = noopDAGRunWorkspaceStore{}
	}
	return &Repository{
		store:             store,
		dagRunWorkspaces:  dagRunWorkspaces,
		latestStatusToday: options.LatestStatusToday,
		location:          location,
		now:               now,
	}
}

// CreateAttempt creates an attempt within a DAG run.
func (r *Repository) CreateAttempt(
	ctx context.Context,
	dag *ir.DAG,
	timestamp time.Time,
	dagRunID string,
	options CreateAttemptOptions,
) (Attempt, error) {
	if dagRunID == "" {
		return nil, ErrDAGRunIDEmpty
	}
	if !options.RootDAGRun.Zero() && options.RootDAGRun.ID == "" {
		return nil, ErrDAGRunIDEmpty
	}
	return r.store.CreateAttempt(ctx, CreateAttemptRequest{
		DAG:        dag,
		RootDAGRun: options.RootDAGRun,
		Timestamp:  timestamp,
		DAGRunID:   dagRunID,
		AttemptID:  options.AttemptID,
		Retry:      options.Retry,
	})
}

// RecentStatuses returns the newest readable status for recent DAG runs.
func (r *Repository) RecentStatuses(ctx context.Context, name string, limit int) []ir.DAGRunStatus {
	if limit <= 0 {
		logger.Warn(ctx, "Invalid itemLimit, using default of 10", tag.Limit(limit))
		limit = 10
	}
	statuses, err := r.store.RecentStatuses(ctx, name, limit)
	if err != nil {
		logger.Error(ctx, "Failed to list recent runs", tag.Error(err))
		return nil
	}

	return statuses
}

// LatestAttempt returns the newest visible attempt for a DAG.
func (r *Repository) LatestAttempt(ctx context.Context, name string, opts ...LatestAttemptOption) (Attempt, error) {
	var options latestAttemptOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	query := LatestAttemptQuery{Name: name}
	if r.latestStatusToday && !options.allHistory {
		now := r.now().In(r.location)
		startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, r.location)
		query.NotBefore = NewUTC(startOfDay)
	}
	return r.store.LatestAttempt(ctx, query)
}

// ListStatuses returns statuses in canonical list order.
func (r *Repository) ListStatuses(ctx context.Context, opts ...ListStatusesOption) ([]*ir.DAGRunStatus, error) {
	page, err := r.store.QueryStatuses(ctx, r.statusQuery(opts))
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

// ListStatusesPage returns one forward-only page in canonical list order.
func (r *Repository) ListStatusesPage(ctx context.Context, opts ...ListStatusesOption) (StatusPage, error) {
	return r.store.QueryStatuses(ctx, r.statusQuery(opts))
}

func (r *Repository) statusQuery(opts []ListStatusesOption) StatusQuery {
	var options listStatusesOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	query := options.query
	if !options.allHistory && query.From.IsZero() && query.To.IsZero() {
		query.From = NewUTC(r.now().Truncate(24 * time.Hour))
	}
	if options.unbounded {
		query.Limit = 0
	} else if query.Limit <= 0 || query.Limit > 1000 {
		query.Limit = 1000
	}
	return query
}

// CompareAndSwapLatestAttemptStatus atomically updates a matching latest attempt.
func (r *Repository) CompareAndSwapLatestAttemptStatus(
	ctx context.Context,
	dagRun ir.DAGRunRef,
	expectedAttemptID string,
	expectedStatus ir.Status,
	mutate func(*ir.DAGRunStatus) error,
	opts ...CompareAndSwapStatusOption,
) (*ir.DAGRunStatus, bool, error) {
	if dagRun.ID == "" {
		return nil, false, ErrDAGRunIDEmpty
	}
	options := newCompareAndSwapStatusOptions(opts...)
	root := options.RootDAGRun
	if root.Zero() {
		root = dagRun
	}
	isSubDAG := root.ID != "" && (root.ID != dagRun.ID || root.Name != dagRun.Name)
	if isSubDAG && root.Name == "" {
		return nil, false, fmt.Errorf("missing root dag-run name for sub dag-run %s", dagRun.ID)
	}
	if root.Name == "" {
		root.Name = dagRun.Name
	}
	if root.ID == "" {
		return nil, false, ErrDAGRunIDEmpty
	}

	return r.store.CompareAndSwapLatestAttemptStatus(ctx, CompareAndSwapStatusRequest{
		DAGRun:             dagRun,
		RootDAGRun:         root,
		ExpectedAttemptID:  expectedAttemptID,
		ExpectedAttemptKey: options.ExpectedAttemptKey,
		ExpectedStatus:     expectedStatus,
		Mutate: func(status *ir.DAGRunStatus) error {
			if err := mutate(status); err != nil {
				return err
			}
			ir.NormalizeDAGRunConditions(status)
			return nil
		},
	})
}

// FindAttempt finds the latest visible attempt for a DAG run.
func (r *Repository) FindAttempt(ctx context.Context, ref ir.DAGRunRef) (Attempt, error) {
	if ref.ID == "" {
		return nil, ErrDAGRunIDEmpty
	}
	return r.store.FindAttempt(ctx, ref)
}

// FindSubAttempt finds the latest visible attempt for a child DAG run.
func (r *Repository) FindSubAttempt(ctx context.Context, root ir.DAGRunRef, childRunID string) (Attempt, error) {
	if root.ID == "" {
		return nil, ErrDAGRunIDEmpty
	}
	return r.store.FindSubAttempt(ctx, root, childRunID)
}

// RemoveOldDAGRuns removes final DAG runs outside the configured retention policy.
func (r *Repository) RemoveOldDAGRuns(
	ctx context.Context,
	name string,
	retentionDays int,
	opts ...RemoveOldDAGRunsOption,
) ([]string, error) {
	var options removeOldDAGRunsOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	request := RetentionRequest{Name: name, DryRun: options.DryRun}
	if options.RetentionRuns != nil {
		if *options.RetentionRuns <= 0 {
			logger.Warn(ctx, "Non-positive retentionRuns, no files will be removed",
				slog.Int("retention-runs", *options.RetentionRuns))
			return nil, nil
		}
		request.KeepRuns = *options.RetentionRuns
		return r.removeOldDAGRuns(ctx, request)
	}
	if options.OlderThan != nil {
		request.OlderThan = NewUTC(*options.OlderThan)
		return r.removeOldDAGRuns(ctx, request)
	}
	if retentionDays < 0 {
		logger.Warn(ctx, "Negative retentionDays, no files will be removed",
			slog.Int("retention-days", retentionDays))
		return nil, nil
	}
	request.OlderThan = NewUTC(r.now().AddDate(0, 0, -retentionDays))
	return r.removeOldDAGRuns(ctx, request)
}

func (r *Repository) removeOldDAGRuns(ctx context.Context, request RetentionRequest) ([]string, error) {
	refs, err := r.store.RemoveOldDAGRuns(ctx, request)
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.ID)
	}
	if request.DryRun {
		return ids, err
	}
	for _, ref := range refs {
		workspaceRef, normalizeErr := normalizeWorkspaceRef(DAGRunWorkspaceRef{DAGRun: ref})
		if normalizeErr != nil {
			err = errors.Join(err, normalizeErr)
			continue
		}
		if removeErr := r.dagRunWorkspaces.Remove(ctx, workspaceRef); removeErr != nil {
			err = errors.Join(err, fmt.Errorf("remove workspace for dag-run %s: %w", ref.ID, removeErr))
		}
	}
	return ids, err
}

// RemoveDAGRun removes a DAG run and all of its attempts.
func (r *Repository) RemoveDAGRun(ctx context.Context, ref ir.DAGRunRef, opts ...RemoveDAGRunOption) error {
	if ref.ID == "" {
		return ErrDAGRunIDEmpty
	}
	var options removeDAGRunOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	err := r.store.RemoveDAGRun(ctx, RemoveDAGRunRequest{
		DAGRun:       ref,
		RejectActive: options.RejectActive,
	})
	if err != nil && !errors.Is(err, ErrDAGRunIDNotFound) {
		return err
	}
	workspaceRef, normalizeErr := normalizeWorkspaceRef(DAGRunWorkspaceRef{DAGRun: ref})
	if normalizeErr != nil {
		return errors.Join(err, normalizeErr)
	}
	removeErr := r.dagRunWorkspaces.Remove(ctx, workspaceRef)
	if removeErr != nil {
		removeErr = fmt.Errorf("remove workspace for dag-run %s: %w", ref.ID, removeErr)
	}
	return errors.Join(err, removeErr)
}

// MaterializeWorkspace makes a DAG-run workspace available locally.
func (r *Repository) MaterializeWorkspace(ctx context.Context, ref DAGRunWorkspaceRef) (string, error) {
	normalized, err := normalizeWorkspaceRef(ref)
	if err != nil {
		return "", err
	}
	return r.dagRunWorkspaces.Materialize(ctx, normalized)
}

// SnapshotWorkspace persists the current state of a DAG-run workspace.
func (r *Repository) SnapshotWorkspace(ctx context.Context, ref DAGRunWorkspaceRef, localDir string) error {
	normalized, err := normalizeWorkspaceRef(ref)
	if err != nil {
		return err
	}
	return r.dagRunWorkspaces.Snapshot(ctx, normalized, localDir)
}

// ListRetryCandidates returns failed latest attempts eligible for retry scanning.
func (r *Repository) ListRetryCandidates(ctx context.Context, from TimeInUTC) ([]*ir.DAGRunStatus, error) {
	if lister, ok := r.store.(RetryCandidateLister); ok {
		return lister.ListRetryCandidates(ctx, from)
	}
	return r.ListStatuses(ctx,
		WithStatuses([]ir.Status{ir.Failed}),
		WithFrom(from),
		WithoutLimit(),
	)
}
