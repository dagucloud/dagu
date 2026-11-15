// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// AbortWaitingRun ends a waiting root DAG-run at stoppedAt without starting an
// attempt. Its waiting steps end aborted, as do the waiting sub-DAG-runs those
// steps wait on. It fails with dagrun.ErrWaitingAbortStateChanged when the run
// has left the waiting checkpoint that status describes.
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

	// Sub-DAG-runs end before the root. A failure leaves the root waiting, so
	// stopping it again completes the stop.
	if err := r.abortWaitingSubRuns(ctx, root, status, abort, map[string]struct{}{}); err != nil {
		return err
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
) error {
	for _, node := range parent.Nodes {
		if node == nil || node.Status != ir.NodeWaiting {
			continue
		}
		for _, subRun := range slices.Concat(node.SubRuns, node.SubRunsRepeated) {
			if _, ok := seen[subRun.DAGRunID]; ok || subRun.DAGRunID == "" {
				continue
			}
			seen[subRun.DAGRunID] = struct{}{}

			attempt, err := r.FindSubAttempt(ctx, root, subRun.DAGRunID)
			if errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
				continue
			}
			if err != nil {
				return fmt.Errorf("find sub dag-run %s: %w", subRun.DAGRunID, err)
			}
			status, err := attempt.ReadStatus(ctx)
			if err != nil {
				return fmt.Errorf("read sub dag-run %s: %w", subRun.DAGRunID, err)
			}
			if status.Status != ir.Waiting {
				continue
			}
			if err := r.abortWaitingSubRuns(ctx, root, status, abort, seen); err != nil {
				return err
			}
			if _, _, err := r.CompareAndSwapLatestAttemptStatus(
				ctx, status.DAGRun(), status.AttemptID, ir.Waiting, abort,
				DAGRunCompareAndSwapOptions{RootDAGRun: root},
			); err != nil {
				return fmt.Errorf("stop waiting sub dag-run %s: %w", subRun.DAGRunID, err)
			}
		}
	}
	return nil
}
