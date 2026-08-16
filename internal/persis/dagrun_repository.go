// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// DAGRunRepository provides application-level access to persisted DAG runs.
type DAGRunRepository struct {
	store             DAGRunStore
	workDirs          dagrun.WorkDirStore
	latestStatusToday bool
	location          *time.Location
	now               func() time.Time
}

// NewDAGRunRepository creates a repository backed by store.
func NewDAGRunRepository(store DAGRunStore, workDirs dagrun.WorkDirStore, options DAGRunRepositoryOptions) *DAGRunRepository {
	location := options.Location
	if location == nil {
		location = time.Local
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	if workDirs == nil {
		workDirs = noopWorkDirStore{}
	}
	return &DAGRunRepository{
		store:             store,
		workDirs:          workDirs,
		latestStatusToday: options.LatestStatusToday,
		location:          location,
		now:               now,
	}
}

// CreateAttempt creates an attempt within a DAG run.
func (r *DAGRunRepository) CreateAttempt(
	ctx context.Context,
	dag *ir.DAG,
	timestamp time.Time,
	dagRunID string,
	options DAGRunCreateAttemptOptions,
) (dagrun.Attempt, error) {
	if dagRunID == "" {
		return nil, dagrun.ErrDAGRunIDEmpty
	}
	if !options.RootDAGRun.Zero() && options.RootDAGRun.ID == "" {
		return nil, dagrun.ErrDAGRunIDEmpty
	}
	attempt, err := r.store.CreateAttempt(ctx, DAGRunCreateAttemptRequest{
		DAG:        dag,
		RootDAGRun: options.RootDAGRun,
		Timestamp:  timestamp,
		DAGRunID:   dagRunID,
		AttemptID:  options.AttemptID,
		Retry:      options.Retry,
	})
	if err != nil {
		return nil, err
	}
	return newEventingAttempt(attempt, dag), nil
}

// RecentStatuses returns the newest readable status for recent DAG runs.
func (r *DAGRunRepository) RecentStatuses(ctx context.Context, name string, limit int) ([]ir.DAGRunStatus, error) {
	if limit <= 0 {
		logger.Warn(ctx, "Non-positive recent DAG-run limit, using default of 10", slog.Int("limit", limit))
		limit = 10
	}
	return r.store.RecentStatuses(ctx, name, limit)
}

// LatestAttempt returns the newest visible attempt for a DAG.
func (r *DAGRunRepository) LatestAttempt(
	ctx context.Context,
	name string,
	options DAGRunLatestAttemptOptions,
) (dagrun.Attempt, error) {
	query := DAGRunLatestAttemptQuery{Name: name}
	if r.latestStatusToday && !options.AllHistory {
		query.NotBefore = NewUTC(r.startOfDay())
	}
	attempt, err := r.store.LatestAttempt(ctx, query)
	if err != nil {
		return nil, err
	}
	return newEventingAttempt(attempt, nil), nil
}

// ListStatuses returns statuses in canonical list order.
func (r *DAGRunRepository) ListStatuses(ctx context.Context, options DAGRunListOptions) ([]*ir.DAGRunStatus, error) {
	page, err := r.store.QueryStatuses(ctx, r.statusQuery(options))
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

// ListStatusesPage returns one forward-only page in canonical list order.
func (r *DAGRunRepository) ListStatusesPage(ctx context.Context, options DAGRunListOptions) (DAGRunStatusPage, error) {
	return r.store.QueryStatuses(ctx, r.statusQuery(options))
}

func (r *DAGRunRepository) statusQuery(options DAGRunListOptions) DAGRunStatusQuery {
	query := DAGRunStatusQuery{
		DAGRunID:        options.DAGRunID,
		Name:            options.Name,
		ExactName:       options.ExactName,
		From:            options.From,
		To:              options.To,
		Statuses:        options.Statuses,
		Limit:           options.Limit,
		Cursor:          options.Cursor,
		Labels:          options.Labels,
		WorkspaceFilter: options.WorkspaceFilter,
	}
	if !options.AllHistory && query.From.IsZero() && query.To.IsZero() {
		query.From = NewUTC(r.startOfDay())
	}
	if options.Unbounded {
		query.Limit = 0
	} else if query.Limit <= 0 || query.Limit > 1000 {
		query.Limit = 1000
	}
	return query
}

func (r *DAGRunRepository) startOfDay() time.Time {
	now := r.now().In(r.location)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, r.location)
}

// CompareAndSwapLatestAttemptStatus atomically updates a matching latest attempt.
func (r *DAGRunRepository) CompareAndSwapLatestAttemptStatus(
	ctx context.Context,
	dagRun ir.DAGRunRef,
	expectedAttemptID string,
	expectedStatus ir.Status,
	mutate func(*ir.DAGRunStatus) error,
	options DAGRunCompareAndSwapOptions,
) (*ir.DAGRunStatus, bool, error) {
	if dagRun.ID == "" {
		return nil, false, dagrun.ErrDAGRunIDEmpty
	}
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
		return nil, false, dagrun.ErrDAGRunIDEmpty
	}

	status, swapped, err := r.store.CompareAndSwapLatestAttemptStatus(ctx, DAGRunCompareAndSwapStatusRequest{
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
	if err != nil || !swapped {
		return status, swapped, err
	}
	r.emitStatusEventAfterSwap(ctx, root, dagRun, expectedStatus, status)
	return status, true, nil
}

// FindAttempt finds the latest visible attempt for a DAG run.
func (r *DAGRunRepository) FindAttempt(ctx context.Context, ref ir.DAGRunRef) (dagrun.Attempt, error) {
	if ref.ID == "" {
		return nil, dagrun.ErrDAGRunIDEmpty
	}
	attempt, err := r.store.FindAttempt(ctx, ref)
	if err != nil {
		return nil, err
	}
	return newEventingAttempt(attempt, nil), nil
}

// FindSubAttempt finds the latest visible attempt for a child DAG run.
func (r *DAGRunRepository) FindSubAttempt(ctx context.Context, root ir.DAGRunRef, childRunID string) (dagrun.Attempt, error) {
	if root.ID == "" {
		return nil, dagrun.ErrDAGRunIDEmpty
	}
	attempt, err := r.store.FindSubAttempt(ctx, root, childRunID)
	if err != nil {
		return nil, err
	}
	return newEventingAttempt(attempt, nil), nil
}

// RemoveOldDAGRuns removes final DAG runs outside the configured retention policy.
func (r *DAGRunRepository) RemoveOldDAGRuns(
	ctx context.Context,
	name string,
	retentionDays int,
	options DAGRunRetentionOptions,
) ([]string, error) {
	request := DAGRunRetentionRequest{Name: name, DryRun: options.DryRun}
	if options.RetentionRuns != nil {
		if *options.RetentionRuns <= 0 {
			logger.Warn(ctx, "Non-positive retentionRuns, no DAG runs will be removed",
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
		logger.Warn(ctx, "Negative retentionDays, no DAG runs will be removed",
			slog.Int("retention-days", retentionDays))
		return nil, nil
	}
	request.OlderThan = NewUTC(r.now().AddDate(0, 0, -retentionDays))
	return r.removeOldDAGRuns(ctx, request)
}

func (r *DAGRunRepository) removeOldDAGRuns(ctx context.Context, request DAGRunRetentionRequest) ([]string, error) {
	refs, err := r.store.RemoveOldDAGRuns(ctx, request)
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.ID)
	}
	if request.DryRun {
		return ids, err
	}
	for _, ref := range refs {
		workDirRef, normalizeErr := normalizeWorkDirRef(dagrun.WorkDirRef{DAGRun: ref})
		if normalizeErr != nil {
			err = errors.Join(err, normalizeErr)
			continue
		}
		if removeErr := r.workDirs.Remove(ctx, workDirRef); removeErr != nil {
			err = errors.Join(err, fmt.Errorf("remove work directory for dag-run %s: %w", ref.ID, removeErr))
		}
	}
	return ids, err
}

// RemoveDAGRun removes a DAG run and all of its attempts.
func (r *DAGRunRepository) RemoveDAGRun(ctx context.Context, ref ir.DAGRunRef, options DAGRunRemoveOptions) error {
	if ref.ID == "" {
		return dagrun.ErrDAGRunIDEmpty
	}
	err := r.store.RemoveDAGRun(ctx, DAGRunRemoveRequest{
		DAGRun:       ref,
		RejectActive: options.RejectActive,
	})
	if err != nil && !errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
		return err
	}
	workDirRef, normalizeErr := normalizeWorkDirRef(dagrun.WorkDirRef{DAGRun: ref})
	if normalizeErr != nil {
		return errors.Join(err, normalizeErr)
	}
	removeErr := r.workDirs.Remove(ctx, workDirRef)
	if removeErr != nil {
		removeErr = fmt.Errorf("remove work directory for dag-run %s: %w", ref.ID, removeErr)
	}
	return errors.Join(err, removeErr)
}
