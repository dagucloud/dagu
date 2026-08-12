// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// DAGRunRepository provides application-level access to persisted DAG runs.
type DAGRunRepository struct {
	store             DAGRunStore
	dagRunWorkspaces  DAGRunWorkspaceStore
	latestStatusToday bool
	location          *time.Location
	now               func() time.Time
}

// NewDAGRunRepository creates a repository backed by store.
func NewDAGRunRepository(store DAGRunStore, dagRunWorkspaces DAGRunWorkspaceStore, options DAGRunRepositoryOptions) *DAGRunRepository {
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
	return &DAGRunRepository{
		store:             store,
		dagRunWorkspaces:  dagRunWorkspaces,
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
func (r *DAGRunRepository) RecentStatuses(ctx context.Context, name string, limit int) []ir.DAGRunStatus {
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
func (r *DAGRunRepository) LatestAttempt(
	ctx context.Context,
	name string,
	options DAGRunLatestAttemptOptions,
) (dagrun.Attempt, error) {
	query := DAGRunLatestAttemptQuery{Name: name}
	if r.latestStatusToday && !options.AllHistory {
		now := r.now().In(r.location)
		startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, r.location)
		query.NotBefore = NewUTC(startOfDay)
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
		query.From = NewUTC(r.now().Truncate(24 * time.Hour))
	}
	if options.Unbounded {
		query.Limit = 0
	} else if query.Limit <= 0 || query.Limit > 1000 {
		query.Limit = 1000
	}
	return query
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
		workspaceRef, normalizeErr := normalizeWorkspaceRef(dagrun.DAGRunWorkspaceRef{DAGRun: ref})
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
	workspaceRef, normalizeErr := normalizeWorkspaceRef(dagrun.DAGRunWorkspaceRef{DAGRun: ref})
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
func (r *DAGRunRepository) MaterializeWorkspace(ctx context.Context, ref dagrun.DAGRunWorkspaceRef) (string, error) {
	normalized, err := normalizeWorkspaceRef(ref)
	if err != nil {
		return "", err
	}
	return r.dagRunWorkspaces.Materialize(ctx, normalized)
}

// SnapshotWorkspace persists the current state of a DAG-run workspace.
func (r *DAGRunRepository) SnapshotWorkspace(ctx context.Context, ref dagrun.DAGRunWorkspaceRef, localDir string) error {
	normalized, err := normalizeWorkspaceRef(ref)
	if err != nil {
		return err
	}
	return r.dagRunWorkspaces.Snapshot(ctx, normalized, localDir)
}

// ListRetryCandidates returns failed latest attempts eligible for retry scanning.
func (r *DAGRunRepository) ListRetryCandidates(ctx context.Context, from TimeInUTC) ([]*ir.DAGRunStatus, error) {
	if lister, ok := r.store.(DAGRunRetryCandidateLister); ok {
		return lister.ListRetryCandidates(ctx, from)
	}
	return r.ListStatuses(ctx, DAGRunListOptions{
		From:      from,
		Statuses:  []ir.Status{ir.Failed},
		Unbounded: true,
	})
}

// ResolveRetryPath resolves the ancestry of a persisted child DAG run.
func (r *DAGRunRepository) ResolveRetryPath(
	ctx context.Context,
	root ir.DAGRunRef,
	targetRunID string,
	stepName string,
) (dagrun.RetryPath, *ir.DAGRunStatus, error) {
	if r == nil {
		return dagrun.RetryPath{}, nil, errors.New("retry path: DAG-run repository is not configured")
	}
	if root.Zero() || targetRunID == "" || stepName == "" {
		return dagrun.RetryPath{}, nil, fmt.Errorf(
			"%w: root run, child run, and step are required",
			dagrun.ErrInvalidRetryPath,
		)
	}

	rootAttempt, err := r.FindAttempt(ctx, root)
	if err != nil {
		return dagrun.RetryPath{}, nil, fmt.Errorf("find root DAG run: %w", err)
	}
	rootStatus, err := readRetryStatus(ctx, rootAttempt)
	if err != nil {
		return dagrun.RetryPath{}, nil, fmt.Errorf("read root DAG run: %w", err)
	}

	targetAttempt, err := r.FindSubAttempt(ctx, root, targetRunID)
	if err != nil {
		return dagrun.RetryPath{}, nil, fmt.Errorf("find child DAG run %s: %w", targetRunID, err)
	}
	targetStatus, err := readRetryStatus(ctx, targetAttempt)
	if err != nil {
		return dagrun.RetryPath{}, nil, fmt.Errorf("read child DAG run %s: %w", targetRunID, err)
	}

	targetNode, err := targetStatus.NodeByName(stepName)
	if err != nil {
		return dagrun.RetryPath{}, nil, fmt.Errorf(
			"%w: %s in DAG run %s",
			dagrun.ErrRetryStepNotFound,
			stepName,
			targetRunID,
		)
	}

	var reversed []dagrun.RetryHop
	current := targetStatus
	seen := make(map[string]struct{})
	for current.DAGRunID != root.ID {
		if _, ok := seen[current.DAGRunID]; ok {
			return dagrun.RetryPath{}, nil, fmt.Errorf(
				"%w: cycle at DAG run %s",
				dagrun.ErrInvalidRetryPath,
				current.DAGRunID,
			)
		}
		seen[current.DAGRunID] = struct{}{}

		parentRef := current.Parent
		if parentRef.ID == "" {
			return dagrun.RetryPath{}, nil, fmt.Errorf(
				"%w: DAG run %s has no parent",
				dagrun.ErrInvalidRetryPath,
				current.DAGRunID,
			)
		}

		var parentStatus *ir.DAGRunStatus
		if parentRef.ID == root.ID {
			parentStatus = rootStatus
		} else {
			parentAttempt, findErr := r.FindSubAttempt(ctx, root, parentRef.ID)
			if findErr != nil {
				return dagrun.RetryPath{}, nil, fmt.Errorf(
					"%w: find parent DAG run %s: %v",
					dagrun.ErrInvalidRetryPath,
					parentRef.ID,
					findErr,
				)
			}
			parentStatus, err = readRetryStatus(ctx, parentAttempt)
			if err != nil {
				return dagrun.RetryPath{}, nil, fmt.Errorf(
					"%w: read parent DAG run %s: %v",
					dagrun.ErrInvalidRetryPath,
					parentRef.ID,
					err,
				)
			}
		}

		node := retryParentNode(parentStatus, current.DAGRunID)
		if node == nil {
			return dagrun.RetryPath{}, nil, fmt.Errorf(
				"%w: parent DAG run %s does not reference child %s",
				dagrun.ErrInvalidRetryPath,
				parentRef.ID,
				current.DAGRunID,
			)
		}
		if node.Step.SubDAG == nil {
			return dagrun.RetryPath{}, nil, fmt.Errorf(
				"%w: step %s in DAG run %s is not a sub-DAG",
				dagrun.ErrInvalidRetryPath,
				node.Step.Name,
				parentRef.ID,
			)
		}
		if node.Step.RepeatPolicy.RepeatMode != "" || len(node.SubRunsRepeated) > 0 {
			return dagrun.RetryPath{}, nil, fmt.Errorf(
				"%w: step %s in DAG run %s repeats",
				dagrun.ErrRepeatingStepTarget,
				node.Step.Name,
				parentRef.ID,
			)
		}
		reversed = append(reversed, dagrun.RetryHop{
			Step:  node.Step.Name,
			RunID: current.DAGRunID,
		})
		current = parentStatus
	}

	if len(reversed) == 0 {
		return dagrun.RetryPath{}, nil, fmt.Errorf(
			"%w: target %s is not a child DAG run",
			dagrun.ErrInvalidRetryPath,
			targetRunID,
		)
	}
	slices.Reverse(reversed)
	return dagrun.RetryPath{Hops: reversed, Step: targetNode.Step.Name}, targetStatus, nil
}

// CancelFailedAutoRetryPendingRun marks the latest eligible failed attempt as aborted.
func (r *DAGRunRepository) CancelFailedAutoRetryPendingRun(
	ctx context.Context,
	status *ir.DAGRunStatus,
) error {
	if !dagrun.CanCancelFailedAutoRetryPendingRun(status) {
		return errors.New("dag-run is not eligible for failed auto-retry cancel")
	}

	updatedStatus, swapped, err := r.CompareAndSwapLatestAttemptStatus(
		ctx,
		status.DAGRun(),
		status.AttemptID,
		ir.Failed,
		func(latest *ir.DAGRunStatus) error {
			latest.Status = ir.Aborted
			return nil
		},
		DAGRunCompareAndSwapOptions{},
	)
	if err != nil {
		return fmt.Errorf("cancel failed auto-retry pending DAG-run: %w", err)
	}
	if swapped {
		return nil
	}

	return &dagrun.FailedAutoRetryCancelStateChangedError{CurrentStatus: updatedStatus}
}

func readRetryStatus(ctx context.Context, attempt dagrun.Attempt) (*ir.DAGRunStatus, error) {
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, err
	}
	if status == nil {
		return nil, dagrun.ErrNoStatusData
	}
	return status, nil
}

func retryParentNode(status *ir.DAGRunStatus, childRunID string) *ir.Node {
	for _, node := range status.Nodes {
		if node == nil {
			continue
		}
		for _, run := range node.SubRuns {
			if run.DAGRunID == childRunID {
				return node
			}
		}
		for _, run := range node.SubRunsRepeated {
			if run.DAGRunID == childRunID {
				return node
			}
		}
	}
	return nil
}

type noopDAGRunWorkspaceStore struct{}

func (noopDAGRunWorkspaceStore) Materialize(context.Context, dagrun.DAGRunWorkspaceRef) (string, error) {
	return "", nil
}

func (noopDAGRunWorkspaceStore) Snapshot(context.Context, dagrun.DAGRunWorkspaceRef, string) error {
	return nil
}

func (noopDAGRunWorkspaceStore) Remove(context.Context, dagrun.DAGRunWorkspaceRef) error {
	return nil
}

func normalizeWorkspaceRef(ref dagrun.DAGRunWorkspaceRef) (dagrun.DAGRunWorkspaceRef, error) {
	if ref.DAGRun.ID == "" {
		return dagrun.DAGRunWorkspaceRef{}, dagrun.ErrDAGRunIDEmpty
	}
	if ref.RootDAGRun.Zero() {
		ref.RootDAGRun = ref.DAGRun
	}
	if ref.RootDAGRun.ID == "" {
		return dagrun.DAGRunWorkspaceRef{}, dagrun.ErrDAGRunIDEmpty
	}
	if ref.RootDAGRun.Name == "" {
		return dagrun.DAGRunWorkspaceRef{}, fmt.Errorf(
			"missing root dag-run name for workspace %s",
			ref.DAGRun.ID,
		)
	}
	if ref.DAGRun.Name == "" && ref.DAGRun.ID == ref.RootDAGRun.ID {
		ref.DAGRun.Name = ref.RootDAGRun.Name
	}
	return ref, nil
}
