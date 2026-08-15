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
	removalEnqueuer   DAGRunRemovalEnqueuer
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
		removalEnqueuer:   options.RemovalEnqueuer,
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
	return r.store.CreateAttempt(ctx, DAGRunCreateAttemptRequest{
		DAG:        dag,
		RootDAGRun: options.RootDAGRun,
		Timestamp:  timestamp,
		DAGRunID:   dagRunID,
		AttemptID:  options.AttemptID,
		Retry:      options.Retry,
	})
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
	return r.store.LatestAttempt(ctx, query)
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

	return r.store.CompareAndSwapLatestAttemptStatus(ctx, DAGRunCompareAndSwapStatusRequest{
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
func (r *DAGRunRepository) FindAttempt(ctx context.Context, ref ir.DAGRunRef) (dagrun.Attempt, error) {
	if ref.ID == "" {
		return nil, dagrun.ErrDAGRunIDEmpty
	}
	return r.store.FindAttempt(ctx, ref)
}

// FindSubAttempt finds the latest visible attempt for a child DAG run.
func (r *DAGRunRepository) FindSubAttempt(ctx context.Context, root ir.DAGRunRef, childRunID string) (dagrun.Attempt, error) {
	if root.ID == "" {
		return nil, dagrun.ErrDAGRunIDEmpty
	}
	return r.store.FindSubAttempt(ctx, root, childRunID)
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
	if !request.DryRun && r.removalEnqueuer != nil {
		preview := request
		preview.DryRun = true
		refs, err := r.store.RemoveOldDAGRuns(ctx, preview)
		if err != nil {
			return nil, err
		}
		if err := r.enqueueDAGRunRemovals(ctx, refs); err != nil {
			return nil, err
		}
		removed := make([]ir.DAGRunRef, 0, len(refs))
		var removeErrs []error
		for _, ref := range refs {
			err := r.store.RemoveDAGRun(ctx, DAGRunRemoveRequest{DAGRun: ref, RejectActive: true})
			if errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
				continue
			}
			if err != nil {
				removeErrs = append(removeErrs, err)
				continue
			}
			removed = append(removed, ref)
		}
		return r.finishDAGRunRemovals(ctx, removed, errors.Join(removeErrs...))
	}
	refs, err := r.store.RemoveOldDAGRuns(ctx, request)
	if request.DryRun {
		return dagRunRefIDs(refs), err
	}
	return r.finishDAGRunRemovals(ctx, refs, err)
}

func (r *DAGRunRepository) finishDAGRunRemovals(ctx context.Context, refs []ir.DAGRunRef, err error) ([]string, error) {
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
	return dagRunRefIDs(refs), err
}

func dagRunRefIDs(refs []ir.DAGRunRef) []string {
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.ID)
	}
	return ids
}

// RemoveDAGRun removes a DAG run and all of its attempts.
func (r *DAGRunRepository) RemoveDAGRun(ctx context.Context, ref ir.DAGRunRef, options DAGRunRemoveOptions) error {
	if ref.ID == "" {
		return dagrun.ErrDAGRunIDEmpty
	}
	if r.removalEnqueuer != nil {
		if err := r.enqueueDAGRunRemovals(ctx, []ir.DAGRunRef{ref}); err != nil {
			return err
		}
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

func (r *DAGRunRepository) enqueueDAGRunRemovals(ctx context.Context, refs []ir.DAGRunRef) error {
	for _, ref := range refs {
		resources, err := r.agentSessionResources(ctx, ref)
		if err != nil {
			if errors.Is(err, dagrun.ErrDAGRunIDNotFound) || errors.Is(err, dagrun.ErrNoStatusData) {
				continue
			}
			return fmt.Errorf("collect agent sessions for dag-run %s: %w", ref.ID, err)
		}
		if err := r.removalEnqueuer.EnqueueDAGRunRemoval(ctx, ref, resources); err != nil {
			return fmt.Errorf("enqueue agent session cleanup for dag-run %s: %w", ref.ID, err)
		}
	}
	return nil
}

func (r *DAGRunRepository) agentSessionResources(ctx context.Context, root ir.DAGRunRef) ([]ir.AgentSessionResource, error) {
	attempt, err := r.store.FindAttempt(ctx, root)
	if err != nil {
		return nil, err
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, err
	}
	resources := ir.MergeAgentSessionResources(status.AgentSessions, status.Nodes)
	seen := map[string]bool{root.ID: true}
	queue := childDAGRunIDs(status.Nodes)
	for len(queue) > 0 {
		childID := queue[0]
		queue = queue[1:]
		if childID == "" || seen[childID] {
			continue
		}
		seen[childID] = true
		childAttempt, err := r.store.FindSubAttempt(ctx, root, childID)
		if err != nil {
			if errors.Is(err, dagrun.ErrDAGRunIDNotFound) || errors.Is(err, dagrun.ErrNoStatusData) {
				continue
			}
			return nil, err
		}
		childStatus, err := childAttempt.ReadStatus(ctx)
		if err != nil {
			if errors.Is(err, dagrun.ErrNoStatusData) {
				continue
			}
			return nil, err
		}
		resources = ir.MergeAgentSessionResources(resources, childStatus.Nodes)
		resources = append(resources, childStatus.AgentSessions...)
		resources = ir.MergeAgentSessionResources(resources, nil)
		queue = append(queue, childDAGRunIDs(childStatus.Nodes)...)
	}
	return resources, nil
}

func childDAGRunIDs(nodes []*ir.Node) []string {
	var ids []string
	for _, node := range nodes {
		if node == nil {
			continue
		}
		for _, child := range node.SubRuns {
			ids = append(ids, child.DAGRunID)
		}
		for _, child := range node.SubRunsRepeated {
			ids = append(ids, child.DAGRunID)
		}
	}
	return ids
}
