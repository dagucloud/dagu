// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"errors"
	"fmt"
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

// RecentStatusesOptions tunes RecentStatusesWithOptions.
type RecentStatusesOptions struct {
	// IncludeEphemeral adds the ephemeral runs of the DAG to the result.
	IncludeEphemeral bool
}

// RecentStatusesWithOptions is RecentStatuses with options. With
// IncludeEphemeral, the ephemeral runs of the DAG are merged with the other
// runs by start time, newest first, under the same limit.
func (r *DAGRunRepository) RecentStatusesWithOptions(
	ctx context.Context,
	name string,
	limit int,
	options RecentStatusesOptions,
) ([]ir.DAGRunStatus, error) {
	limit = recentLimit(ctx, limit)
	statuses, err := r.RecentStatuses(ctx, name, limit)
	if err != nil || !options.IncludeEphemeral || r.ephemeral == nil {
		return statuses, err
	}
	ephemeral, err := r.ephemeral.RecentStatuses(ctx, name, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent ephemeral runs of %s: %w", name, err)
	}
	if len(ephemeral) == 0 {
		return statuses, nil
	}
	return mergeRecentStatuses(statuses, ephemeral, limit), nil
}

// mergeRecentStatuses merges two lists that are each newest first, keeping
// the order of each list, and keeps at most limit entries.
func mergeRecentStatuses(a, b []ir.DAGRunStatus, limit int) []ir.DAGRunStatus {
	merged := make([]ir.DAGRunStatus, 0, min(len(a)+len(b), limit))
	for len(merged) < limit && (len(a) > 0 || len(b) > 0) {
		switch {
		case len(b) == 0:
			merged, a = append(merged, a[0]), a[1:]
		case len(a) == 0:
			merged, b = append(merged, b[0]), b[1:]
		case startedAt(&b[0]).After(startedAt(&a[0])):
			merged, b = append(merged, b[0]), b[1:]
		default:
			merged, a = append(merged, a[0]), a[1:]
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
