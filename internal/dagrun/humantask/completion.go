// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package humantask

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

// Complete validates and durably completes one human task, then queues the run when possible.
func (s *Service) Complete(ctx context.Context, request CompleteRequest) (Result, error) {
	s.defaults()
	if s.DAGRunStore == nil {
		return Result{}, errorf(ErrorInternal, "DAG-run store is not configured")
	}
	request.StepID = strings.TrimSpace(request.StepID)
	if request.StepID == "" {
		return Result{}, errorf(ErrorInvalid, "human task step ID must not be empty")
	}

	target, err := s.loadTarget(ctx, request.DAGName, request.DAGRunID, request.StepID)
	if err != nil {
		return Result{}, err
	}
	node, err := findNodeByID(target.status.Nodes, request.StepID)
	if err != nil {
		return Result{}, err
	}
	completion, outputsValue, err := prepareCompletion(target.dag, node, request.Input)
	if err != nil {
		return Result{}, err
	}

	if nodeCompleted(node) {
		if !bytes.Equal(node.HumanTaskInput, completion.Canonical) {
			return Result{}, errorf(ErrorConflict, "human task step %q was already completed with different input", request.StepID)
		}
		result := resultFor(target.status, request.StepID, true)
		if hasWaitingNodes(target.status.Nodes) {
			return result, nil
		}
		if target.status.Status == core.Waiting ||
			(target.status.Status == core.Queued && target.status.RetryQueueKey != "") {
			return s.queueCompletedTaskResume(ctx, target)
		}
		return result, nil
	}

	if target.status.Status != core.Waiting {
		return Result{}, errorf(
			ErrorConflict,
			"DAG-run %s is not waiting (status: %s)",
			target.ref,
			target.status.Status,
		)
	}

	completedAt := s.Now().UTC().Format(time.RFC3339)
	var concurrentlyCompleted *exec.DAGRunStatus
	updated, swapped, err := s.DAGRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		target.ref,
		target.status.AttemptID,
		core.Waiting,
		func(latest *exec.DAGRunStatus) error {
			latestNode, err := findNodeByID(latest.Nodes, request.StepID)
			if err != nil {
				return err
			}
			if nodeCompleted(latestNode) {
				if !bytes.Equal(latestNode.HumanTaskInput, completion.Canonical) {
					return errorf(ErrorConflict, "human task step %q was already completed with different input", request.StepID)
				}
				concurrentlyCompleted = latest
				return errCompletionAlreadyApplied
			}
			if latestNode.Status != core.NodeWaiting {
				return errorf(
					ErrorConflict,
					"human task step %q is not waiting (status: %s)",
					request.StepID,
					latestNode.Status,
				)
			}

			latestNode.HumanTaskInput = append(json.RawMessage(nil), completion.Canonical...)
			latestNode.HumanTaskCompletedBy = request.CompletedBy
			latestNode.HumanTaskCompletedByID = request.CompletedByID
			if outputsValue == "" {
				latestNode.StepOutputsValue = nil
			} else {
				latestNode.StepOutputsValue = &outputsValue
			}
			latestNode.FinishedAt = completedAt
			latestNode.Status = core.NodeSucceeded
			return nil
		},
		exec.WithCompareAndSwapExpectedAttemptKey(target.status.AttemptKey),
	)
	if errors.Is(err, errCompletionAlreadyApplied) {
		return s.queueCompletedTaskResume(ctx, target.withStatus(concurrentlyCompleted))
	}
	if err != nil {
		return Result{}, classifyMutationError("failed to complete human task", err)
	}
	if !swapped {
		return s.resolveCompletionConflict(ctx, target, updated, completion.Canonical)
	}

	result := resultFor(updated, request.StepID, false)
	if result.RemainingWaitingSteps > 0 {
		return result, nil
	}
	return s.enqueueResume(ctx, target.withStatus(updated), result)
}

func (s *Service) queueCompletedTaskResume(ctx context.Context, target *target) (Result, error) {
	result := resultFor(target.status, target.stepID, true)
	return s.enqueueResume(ctx, target, result)
}

func (s *Service) resolveCompletionConflict(
	ctx context.Context,
	target *target,
	updated *exec.DAGRunStatus,
	canonical json.RawMessage,
) (Result, error) {
	if updated != nil {
		latestNode, err := findNodeByID(updated.Nodes, target.stepID)
		if err == nil && nodeCompleted(latestNode) {
			if !bytes.Equal(latestNode.HumanTaskInput, canonical) {
				return Result{}, errorf(ErrorConflict, "human task step %q was already completed with different input", target.stepID)
			}
			return s.queueCompletedTaskResume(ctx, target.withStatus(updated))
		}
	}
	return Result{}, errorf(
		ErrorConflict,
		"DAG-run changed while completing human task %q; inspect its current status and retry",
		target.stepID,
	)
}
