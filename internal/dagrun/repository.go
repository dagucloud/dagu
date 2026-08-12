// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
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
	latestStatusToday bool
	location          *time.Location
	now               func() time.Time
}

// NewRepository creates a repository backed by store.
func NewRepository(store Store, options RepositoryOptions) *Repository {
	location := options.Location
	if location == nil {
		location = time.Local
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Repository{
		store:             store,
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
	if options.RootDAGRun != nil && options.RootDAGRun.ID == "" {
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
	attempts, err := r.store.RecentAttempts(ctx, name, limit)
	if err != nil {
		logger.Error(ctx, "Failed to list recent runs", tag.Error(err))
		return nil
	}

	statuses := make([]ir.DAGRunStatus, 0, len(attempts))
	for _, attempt := range attempts {
		status, err := attempt.ReadStatus(ctx)
		if err == nil {
			statuses = append(statuses, *status)
		}
	}
	return statuses
}

// LatestAttempt returns the newest visible attempt for a DAG.
func (r *Repository) LatestAttempt(ctx context.Context, name string) (Attempt, error) {
	query := LatestAttemptQuery{Name: name}
	if r.latestStatusToday {
		now := r.now().In(r.location)
		startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, r.location)
		query.NotBefore = NewUTC(startOfDay)
	}
	return r.store.LatestAttempt(ctx, query)
}

// ListStatuses returns statuses in canonical list order.
func (r *Repository) ListStatuses(ctx context.Context, opts ...ListDAGRunStatusesOption) ([]*ir.DAGRunStatus, error) {
	page, err := r.store.QueryStatuses(ctx, r.statusQuery(opts))
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

// ListStatusesPage returns one forward-only page in canonical list order.
func (r *Repository) ListStatusesPage(ctx context.Context, opts ...ListDAGRunStatusesOption) (DAGRunStatusPage, error) {
	return r.store.QueryStatuses(ctx, r.statusQuery(opts))
}

func (r *Repository) statusQuery(opts []ListDAGRunStatusesOption) StatusQuery {
	var query StatusQuery
	for _, opt := range opts {
		if opt != nil {
			opt(&query)
		}
	}
	if !query.AllHistory && query.From.IsZero() && query.To.IsZero() {
		query.From = NewUTC(r.now().Truncate(24 * time.Hour))
	}
	if !query.Unlimited && (query.Limit == 0 || query.Limit > 1000) {
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
		return r.store.RemoveOldDAGRuns(ctx, request)
	}
	if options.OlderThan != nil {
		request.OlderThan = NewUTC(*options.OlderThan)
		return r.store.RemoveOldDAGRuns(ctx, request)
	}
	if retentionDays < 0 {
		logger.Warn(ctx, "Negative retentionDays, no files will be removed",
			slog.Int("retention-days", retentionDays))
		return nil, nil
	}
	request.OlderThan = NewUTC(r.now().AddDate(0, 0, -retentionDays))
	return r.store.RemoveOldDAGRuns(ctx, request)
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
	return r.store.RemoveDAGRun(ctx, RemoveDAGRunRequest{
		DAGRun:       ref,
		RejectActive: options.RejectActive,
	})
}

// ListRetryCandidates returns failed latest attempts eligible for retry scanning.
func (r *Repository) ListRetryCandidates(ctx context.Context, from TimeInUTC) ([]*ir.DAGRunStatus, error) {
	if store, ok := r.store.(RetryCandidateStore); ok {
		return store.ListRetryCandidates(ctx, from)
	}
	return r.ListStatuses(ctx,
		WithStatuses([]ir.Status{ir.Failed}),
		WithFrom(from),
		WithoutLimit(),
	)
}
