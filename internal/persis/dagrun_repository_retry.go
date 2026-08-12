// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

type dagRunRetryCandidateLister interface {
	ListRetryCandidates(ctx context.Context, from TimeInUTC) ([]*ir.DAGRunStatus, error)
}

// ListRetryCandidates returns failed latest attempts eligible for retry scanning.
func (r *DAGRunRepository) ListRetryCandidates(ctx context.Context, from TimeInUTC) ([]*ir.DAGRunStatus, error) {
	if lister, ok := r.store.(dagRunRetryCandidateLister); ok {
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
