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

// AbortWaitingRun ends a waiting root DAG-run at stoppedAt without starting an
// attempt. Its waiting steps end aborted, as do the waiting sub-DAG-runs those
// steps wait on. It fails with dagrun.ErrWaitingAbortStateChanged, changing
// nothing, when the run has left the waiting checkpoint that status describes.
func (r *DAGRunRepository) AbortWaitingRun(ctx context.Context, status *ir.DAGRunStatus, stoppedAt time.Time) error {
	if status == nil || status.Status != ir.Waiting {
		return dagrun.ErrWaitingAbortStateChanged
	}
	if !status.Parent.Zero() {
		return dagrun.ErrWaitingAbortNotRoot
	}
	root := status.DAGRun()
	finishedAt := stringutil.FormatTime(stoppedAt)
	abort := func(latest *ir.DAGRunStatus) error {
		dagrun.AbortWaiting(latest, finishedAt)
		return nil
	}

	latest, swapped, err := r.CompareAndSwapLatestAttemptStatus(
		ctx, root, status.AttemptID, ir.Waiting, abort, DAGRunCompareAndSwapOptions{},
	)
	if err != nil {
		return fmt.Errorf("stop waiting dag-run %s: %w", root, err)
	}
	if !swapped {
		if latest == nil {
			return dagrun.ErrWaitingAbortStateChanged
		}
		return fmt.Errorf("%w (status: %s)", dagrun.ErrWaitingAbortStateChanged, latest.Status)
	}

	// Sub-DAG-runs end after the root: a sub-DAG-run of a root that is no
	// longer waiting cannot resume, so a failure to end one does not undo the
	// stop.
	r.abortWaitingSubRuns(ctx, root, latest, abort, map[string]struct{}{})
	return nil
}

// abortWaitingSubRuns ends the waiting sub-DAG-runs of the waiting steps of
// parent, deepest first. A sub-DAG-run that left its checkpoint meanwhile is
// left to the action that moved it.
func (r *DAGRunRepository) abortWaitingSubRuns(
	ctx context.Context,
	root ir.DAGRunRef,
	parent *ir.DAGRunStatus,
	abort func(*ir.DAGRunStatus) error,
	seen map[string]struct{},
) {
	for _, node := range parent.Nodes {
		// The root's waiting steps are already aborted when its sub-DAG-runs end.
		if node == nil || (node.Status != ir.NodeWaiting && node.Status != ir.NodeAborted) {
			continue
		}
		for _, subRun := range slices.Concat(node.SubRuns, node.SubRunsRepeated) {
			if _, ok := seen[subRun.DAGRunID]; ok || subRun.DAGRunID == "" {
				continue
			}
			seen[subRun.DAGRunID] = struct{}{}
			if err := r.abortWaitingSubRun(ctx, root, subRun.DAGRunID, abort, seen); err != nil {
				logger.Warn(ctx, "Failed to stop a waiting sub dag-run",
					tag.SubRunID(subRun.DAGRunID),
					tag.Error(err),
				)
			}
		}
	}
}

func (r *DAGRunRepository) abortWaitingSubRun(
	ctx context.Context,
	root ir.DAGRunRef,
	runID string,
	abort func(*ir.DAGRunStatus) error,
	seen map[string]struct{},
) error {
	attempt, err := r.FindSubAttempt(ctx, root, runID)
	if errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find sub dag-run: %w", err)
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return fmt.Errorf("read sub dag-run: %w", err)
	}
	if status.Status != ir.Waiting {
		return nil
	}
	r.abortWaitingSubRuns(ctx, root, status, abort, seen)
	if _, _, err := r.CompareAndSwapLatestAttemptStatus(
		ctx, status.DAGRun(), status.AttemptID, ir.Waiting, abort,
		DAGRunCompareAndSwapOptions{RootDAGRun: root},
	); err != nil {
		return fmt.Errorf("stop waiting sub dag-run: %w", err)
	}
	return nil
}
