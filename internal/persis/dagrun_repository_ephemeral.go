// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// EphemeralRunTTL is how long an ephemeral run stays on disk after it ends.
const EphemeralRunTTL = 10 * time.Minute

// ErrEphemeralUnsupported reports an ephemeral run requested from a
// repository that has no ephemeral store.
var ErrEphemeralUnsupported = errors.New("ephemeral runs are not supported by this DAG-run store")

// createStore picks the store that receives a new attempt. A new ephemeral
// root run goes to the ephemeral store. A retry or a child run follows the
// root run it belongs to.
func (r *DAGRunRepository) createStore(
	ctx context.Context,
	dag *ir.DAG,
	dagRunID string,
	options DAGRunCreateAttemptOptions,
) (DAGRunStore, bool, error) {
	newRoot := !options.Retry && options.RootDAGRun.Zero()
	if options.Ephemeral && newRoot {
		if r.ephemeral == nil {
			return nil, false, ErrEphemeralUnsupported
		}
		if dag != nil {
			if err := rejectExistingRun(ctx, r.store, ir.NewDAGRunRef(dag.Name, dagRunID)); err != nil {
				return nil, false, err
			}
		}
		return r.ephemeral, true, nil
	}
	if r.ephemeral == nil || dag == nil {
		return r.store, false, nil
	}
	if newRoot {
		if err := rejectExistingRun(ctx, r.ephemeral, ir.NewDAGRunRef(dag.Name, dagRunID)); err != nil {
			return nil, false, err
		}
		return r.store, false, nil
	}
	root := options.RootDAGRun
	if root.Zero() {
		root = ir.NewDAGRunRef(dag.Name, dagRunID)
	}
	store, ephemeral := r.storeHolding(ctx, root)
	return store, ephemeral, nil
}

// rejectExistingRun fails when store holds the run, so that a run ID names one
// run across the history and ephemeral stores.
func rejectExistingRun(ctx context.Context, store DAGRunStore, ref ir.DAGRunRef) error {
	_, err := store.FindAttempt(ctx, ref)
	if err == nil || errors.Is(err, dagrun.ErrNoStatusData) {
		return fmt.Errorf("%w: %s", dagrun.ErrDAGRunAlreadyExists, ref.ID)
	}
	return nil
}

// storeHolding returns the store that holds the root run, and whether that
// is the ephemeral store. A run found in neither is reported against the
// history store.
func (r *DAGRunRepository) storeHolding(ctx context.Context, root ir.DAGRunRef) (DAGRunStore, bool) {
	if r.ephemeral == nil || root.ID == "" {
		return r.store, false
	}
	_, err := r.ephemeral.FindAttempt(ctx, root)
	if err == nil || errors.Is(err, dagrun.ErrNoStatusData) {
		return r.ephemeral, true
	}
	return r.store, false
}

// ListEphemeralRuns returns the status of every ephemeral root run.
func (r *DAGRunRepository) ListEphemeralRuns(ctx context.Context) ([]*ir.DAGRunStatus, error) {
	if r.ephemeral == nil {
		return nil, nil
	}
	page, err := r.ephemeral.QueryStatuses(ctx, DAGRunStatusQuery{})
	if err != nil {
		return nil, fmt.Errorf("list ephemeral runs: %w", err)
	}
	return page.Items, nil
}

// RemoveEndedEphemeralRuns removes the ephemeral runs that ended at least
// ttl ago, and returns how many it removed. A run that has not ended is never
// removed. A zero ttl removes every ended run.
func (r *DAGRunRepository) RemoveEndedEphemeralRuns(ctx context.Context, ttl time.Duration) (int, error) {
	statuses, err := r.ListEphemeralRuns(ctx)
	if err != nil {
		return 0, err
	}
	cutoff := r.now().Add(-ttl)
	removed := 0
	var errs []error
	for _, status := range statuses {
		if status == nil || status.Status.IsActive() || status.Status == ir.NotStarted {
			continue
		}
		if endedAt(status).After(cutoff) {
			continue
		}
		// A retry may have started since the listing; the store checks the
		// attempt again under its lock.
		err := r.RemoveDAGRun(ctx, status.DAGRun(), DAGRunRemoveOptions{
			RejectActive:      true,
			ExpectedAttemptID: status.AttemptID,
		})
		switch {
		case err == nil:
			removed++
		case errors.Is(err, dagrun.ErrDAGRunActive), errors.Is(err, dagrun.ErrDAGRunIDNotFound):
		default:
			logger.Warn(ctx, "Failed to remove ephemeral run",
				tag.DAG(status.Name), tag.RunID(status.DAGRunID), tag.Error(err))
			errs = append(errs, err)
		}
	}
	return removed, errors.Join(errs...)
}

// endedAt returns when an ended run finished, falling back to the latest
// time the status records when the finish time is missing.
func endedAt(status *ir.DAGRunStatus) time.Time {
	for _, value := range []string{status.FinishedAt, status.StartedAt} {
		if t, err := stringutil.ParseTime(value); err == nil && !t.IsZero() {
			return t
		}
	}
	if status.CreatedAt > 0 {
		return time.UnixMilli(status.CreatedAt)
	}
	return time.Time{}
}

// RecentStatusesPageOptions selects one page of RecentStatusesPage.
type RecentStatusesPageOptions struct {
	// Limit is the most runs of the history that the page holds.
	Limit int
	// Cursor is the NextCursor of the previous page; empty for the first page.
	Cursor string
	// IncludeEphemeral adds the ephemeral runs of the DAG to the first page.
	IncludeEphemeral bool
	// ExcludeBackfill leaves out backfill runs, unless the DAG has no other
	// runs, so a long backfill does not push the live runs out of the page.
	ExcludeBackfill bool
}

// RecentStatusesPage returns one page of the runs of a DAG, newest first,
// and the cursor of the next page of older runs. With IncludeEphemeral, the
// first page also holds the ephemeral runs of the DAG that are not older
// than its oldest run, merged by start time; no later page holds one.
func (r *DAGRunRepository) RecentStatusesPage(
	ctx context.Context,
	name string,
	options RecentStatusesPageOptions,
) (DAGRunStatusPage, error) {
	limit := recentLimit(ctx, options.Limit)
	list := DAGRunListOptions{
		ExactName:       name,
		Limit:           limit,
		Cursor:          options.Cursor,
		AllHistory:      true,
		FullStatus:      true,
		ExcludeBackfill: options.ExcludeBackfill,
	}
	page, err := r.ListStatusesPage(ctx, list)
	switch {
	case err == nil && options.ExcludeBackfill && options.Cursor == "" && len(page.Items) == 0,
		errors.Is(err, ErrInvalidDAGRunQueryCursor) && options.ExcludeBackfill:
		// A DAG whose only runs are backfill runs shows them. The cursor of
		// such a page records that it lists them, so later pages do too.
		list.ExcludeBackfill = false
		page, err = r.ListStatusesPage(ctx, list)
	}
	if err != nil || options.Cursor != "" || !options.IncludeEphemeral || r.ephemeral == nil {
		return page, err
	}
	ephemeral, err := r.ephemeral.RecentStatuses(ctx, name, limit)
	if err != nil {
		return DAGRunStatusPage{}, fmt.Errorf("list recent ephemeral runs of %s: %w", name, err)
	}
	// An older ephemeral run would sort among the runs of the next page.
	if page.NextCursor != "" && len(page.Items) > 0 {
		oldest := startedAt(page.Items[len(page.Items)-1])
		ephemeral = slices.DeleteFunc(ephemeral, func(status ir.DAGRunStatus) bool {
			return startedAt(&status).Before(oldest)
		})
	}
	page.Items = mergeRecentStatuses(page.Items, ephemeral)
	return page, nil
}

// mergeRecentStatuses merges runs of the history and ephemeral runs, each
// newest first, keeping the order of each list.
func mergeRecentStatuses(runs []*ir.DAGRunStatus, ephemeral []ir.DAGRunStatus) []*ir.DAGRunStatus {
	merged := make([]*ir.DAGRunStatus, 0, len(runs)+len(ephemeral))
	for len(runs) > 0 || len(ephemeral) > 0 {
		switch {
		case len(ephemeral) == 0:
			merged, runs = append(merged, runs[0]), runs[1:]
		case len(runs) == 0 || startedAt(&ephemeral[0]).After(startedAt(runs[0])):
			merged, ephemeral = append(merged, &ephemeral[0]), ephemeral[1:]
		default:
			merged, runs = append(merged, runs[0]), runs[1:]
		}
	}
	return merged
}

// startedAt returns when a run started, falling back to when it was queued
// or created for a run that has not started.
func startedAt(status *ir.DAGRunStatus) time.Time {
	for _, value := range []string{status.StartedAt, status.QueuedAt} {
		if t, err := stringutil.ParseTime(value); err == nil && !t.IsZero() {
			return t
		}
	}
	if status.CreatedAt > 0 {
		return time.UnixMilli(status.CreatedAt)
	}
	return time.Time{}
}
