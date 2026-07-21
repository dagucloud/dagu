// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package humantask

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/google/uuid"
)

// Resume retries a pending human-task resume without requiring the submitted form values.
func (s *Service) Resume(ctx context.Context, dagName, dagRunID string) (Result, error) {
	s.defaults()
	if s.DAGRunStore == nil {
		return Result{}, errorf(ErrorInternal, "DAG-run store is not configured")
	}
	target, err := s.loadTarget(ctx, dagName, dagRunID, "")
	if err != nil {
		return Result{}, err
	}
	if target.status.Status != core.Waiting {
		if !hasCompletedHumanTask(target.status.Nodes) {
			return Result{}, errorf(ErrorConflict, "DAG-run %s has no completed human-task checkpoint to resume", target.ref)
		}
		return resultFor(target.status, "", true), nil
	}
	if hasWaitingNodes(target.status.Nodes) {
		return Result{}, errorf(ErrorConflict, "DAG-run %s still has manual steps waiting for input", target.ref)
	}
	if !hasCompletedHumanTask(target.status.Nodes) {
		return Result{}, errorf(ErrorConflict, "DAG-run %s has no completed human-task checkpoint to resume", target.ref)
	}
	status, adopted, err := s.ensureResumePending(ctx, target)
	if err != nil {
		return Result{}, err
	}
	result := resultFor(status, "", true)
	return s.resume(ctx, target.withStatus(status), result, adopted)
}

func (s *Service) ensureResumePending(ctx context.Context, target *target) (*exec.DAGRunStatus, bool, error) {
	if target.status == nil || target.status.Status != core.Waiting || hasWaitingNodes(target.status.Nodes) {
		return target.status, false, nil
	}
	if target.status.HumanTaskResume != nil {
		return target.status, false, nil
	}
	if !hasCompletedHumanTask(target.status.Nodes) {
		return nil, false, errorf(ErrorConflict, "DAG-run %s has no completed human-task checkpoint to resume", target.ref)
	}
	requestedAt := s.Now().UTC().Format(time.RFC3339)
	created := false
	updated, swapped, err := s.DAGRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		target.ref,
		target.status.AttemptID,
		core.Waiting,
		func(latest *exec.DAGRunStatus) error {
			if hasWaitingNodes(latest.Nodes) {
				return errorf(ErrorConflict, "DAG-run %s still has manual steps waiting for input", target.ref)
			}
			if latest.HumanTaskResume == nil {
				latest.HumanTaskResume = &exec.HumanTaskResumeState{RequestedAt: requestedAt}
				created = true
			}
			return nil
		},
		exec.WithCompareAndSwapExpectedAttemptKey(target.status.AttemptKey),
	)
	if err != nil {
		return nil, false, classifyMutationError("failed to persist human-task resume state", err)
	}
	if !swapped {
		return nil, false, errorf(ErrorConflict, "DAG-run changed while preparing human-task resume")
	}
	return updated, created, nil
}

func (s *Service) resume(
	ctx context.Context,
	target *target,
	result Result,
	adoptedLegacyState bool,
) (Result, error) {
	if target.status == nil || target.status.Status != core.Waiting || hasWaitingNodes(target.status.Nodes) {
		return result, nil
	}
	postCommitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.HandoffTimeout)
	defer cancel()

	claim, err := s.claimResume(postCommitCtx, target, adoptedLegacyState)
	if err != nil {
		return result, err
	}
	if !claim.claimed {
		return result, nil
	}

	resumeTarget := target.withStatus(claim.status)
	if err := s.handoff(postCommitCtx, resumeTarget); err != nil {
		if releaseErr := s.releaseClaim(ctx, resumeTarget, claim.token); releaseErr != nil {
			err = fmt.Errorf("%w; failed to release resume claim: %v", err, releaseErr)
		}
		return result, &ResumeError{Result: result, Err: err}
	}
	result.ResumeRequested = true
	return result, nil
}

func (s *Service) claimResume(
	ctx context.Context,
	target *target,
	adoptedLegacyState bool,
) (resumeClaim, error) {
	state := target.status.HumanTaskResume
	if state == nil {
		return resumeClaim{}, errorf(ErrorConflict, "DAG-run %s has no pending human-task resume", target.ref)
	}
	stale := claimExpired(state, s.Now(), s.ClaimLease)
	if state.ClaimToken != "" && !stale {
		return resumeClaim{status: target.status}, nil
	}
	if adoptedLegacyState || stale {
		active, err := s.handoffActive(ctx, target)
		if err != nil {
			return resumeClaim{}, errorf(ErrorInternal, "failed to check existing human-task resume: %v", err)
		}
		if active {
			return resumeClaim{status: target.status}, nil
		}
	}

	token := uuid.NewString()
	claimedAt := s.Now().UTC().Format(time.RFC3339)
	claimed := false
	updated, swapped, err := s.DAGRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		target.ref,
		target.status.AttemptID,
		core.Waiting,
		func(latest *exec.DAGRunStatus) error {
			if hasWaitingNodes(latest.Nodes) || latest.HumanTaskResume == nil {
				return errorf(ErrorConflict, "DAG-run changed while claiming human-task resume")
			}
			latestState := latest.HumanTaskResume
			if latestState.ClaimToken != "" && !claimExpired(latestState, s.Now(), s.ClaimLease) {
				return nil
			}
			latestState.ClaimToken = token
			latestState.ClaimedAt = claimedAt
			claimed = true
			return nil
		},
		exec.WithCompareAndSwapExpectedAttemptKey(target.status.AttemptKey),
	)
	if err != nil {
		return resumeClaim{}, classifyMutationError("failed to claim human-task resume", err)
	}
	if !swapped {
		return resumeClaim{}, errorf(ErrorConflict, "DAG-run changed while claiming human-task resume")
	}
	return resumeClaim{claimed: claimed, status: updated, token: token}, nil
}

func claimExpired(state *exec.HumanTaskResumeState, now time.Time, lease time.Duration) bool {
	if state == nil || state.ClaimToken == "" {
		return false
	}
	claimedAt, err := time.Parse(time.RFC3339, state.ClaimedAt)
	if err != nil {
		return true
	}
	return !claimedAt.Add(lease).After(now)
}

func (s *Service) handoff(ctx context.Context, target *target) error {
	if exec.IsRemoteWorkerID(target.status.WorkerID) {
		if s.QueueStore == nil {
			return errors.New("queue store is not configured")
		}
		if err := s.waitForRemoteDispatch(ctx, target.dag, target.status); err != nil {
			return err
		}
		if err := exec.EnqueueRetry(ctx, s.DAGRunStore, s.QueueStore, target.dag, target.status, exec.EnqueueRetryOptions{}); err != nil {
			return fmt.Errorf("enqueue distributed retry: %w", err)
		}
		return nil
	}
	if s.LocalResumer == nil {
		return errors.New("local human-task resumer is not configured")
	}
	return s.LocalResumer.ResumeHumanTask(ctx, target.dag, target.status)
}

func (s *Service) handoffActive(ctx context.Context, target *target) (bool, error) {
	if exec.IsRemoteWorkerID(target.status.WorkerID) {
		if s.QueueStore == nil {
			return false, errors.New("queue store is not configured")
		}
		return queuedRunExists(ctx, s.QueueStore, target.dag, target.status)
	}
	if s.ProcStore == nil {
		return false, errors.New("process store is not configured")
	}
	return s.ProcStore.IsRunAlive(ctx, target.dag.ProcGroup(), target.status.DAGRun())
}

func (s *Service) releaseClaim(ctx context.Context, target *target, token string) error {
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.SettleTimeout)
	defer cancel()
	_, swapped, err := s.DAGRunStore.CompareAndSwapLatestAttemptStatus(
		rollbackCtx,
		target.ref,
		target.status.AttemptID,
		core.Waiting,
		func(latest *exec.DAGRunStatus) error {
			if latest.HumanTaskResume != nil && latest.HumanTaskResume.ClaimToken == token {
				latest.HumanTaskResume.ClaimToken = ""
				latest.HumanTaskResume.ClaimedAt = ""
			}
			return nil
		},
		exec.WithCompareAndSwapExpectedAttemptKey(target.status.AttemptKey),
	)
	if err != nil {
		return err
	}
	if !swapped {
		return errors.New("DAG-run state changed before the resume claim could be released")
	}
	return nil
}

func (s *Service) waitForCompletionReady(
	ctx context.Context,
	attempt exec.DAGRunAttempt,
	dag *core.DAG,
	status *exec.DAGRunStatus,
	stepID string,
) (*exec.DAGRunStatus, error) {
	if status.Status != core.Waiting || status.AttemptID == "" {
		return status, nil
	}
	originalAttemptID := status.AttemptID
	if !exec.IsRemoteWorkerID(status.WorkerID) && s.ProcStore != nil {
		deadline := s.Now().Add(s.SettleTimeout)
		for {
			alive, err := s.ProcStore.IsAttemptAlive(ctx, dag.ProcGroup(), status.DAGRun(), status.AttemptID)
			if err != nil {
				return nil, errorf(ErrorInternal, "failed to check whether DAG-run attempt is still finalizing: %v", err)
			}
			if !alive {
				break
			}
			if !s.Now().Before(deadline) {
				return nil, errorf(ErrorConflict, "DAG-run attempt %s is still finalizing; retry human-task completion", status.AttemptID)
			}
			if err := s.waitForPoll(ctx); err != nil {
				return nil, err
			}
		}
		latest, err := reloadStatus(ctx, attempt)
		if err != nil {
			return nil, err
		}
		status = latest
	}

	deadline := s.Now().Add(s.SettleTimeout)
	for {
		finalizing, err := attemptFinalizing(status, originalAttemptID, stepID)
		if err != nil {
			return nil, err
		}
		if !finalizing {
			return status, nil
		}
		if !s.Now().Before(deadline) {
			return nil, errorf(ErrorConflict, "DAG-run attempt %s is still finalizing; retry human-task completion", originalAttemptID)
		}
		if err := s.waitForPoll(ctx); err != nil {
			return nil, err
		}
		status, err = reloadStatus(ctx, attempt)
		if err != nil {
			return nil, err
		}
	}
}

func (s *Service) waitForRemoteDispatch(ctx context.Context, dag *core.DAG, status *exec.DAGRunStatus) error {
	deadline := s.Now().Add(s.SettleTimeout)
	for {
		pending, err := queuedRunExists(ctx, s.QueueStore, dag, status)
		if err != nil {
			return fmt.Errorf("check previous distributed dispatch: %w", err)
		}
		if !pending {
			return nil
		}
		if !s.Now().Before(deadline) {
			return errors.New("previous distributed dispatch is still finalizing")
		}
		if err := s.waitForPoll(ctx); err != nil {
			return err
		}
	}
}

func queuedRunExists(ctx context.Context, queueStore exec.QueueStore, dag *core.DAG, status *exec.DAGRunStatus) (bool, error) {
	queueName := status.ProcGroup
	if queueName == "" {
		queueName = dag.ProcGroup()
	}
	items, err := queueStore.ListByDAGName(ctx, queueName, status.Name)
	if err != nil {
		return false, err
	}
	for _, item := range items {
		ref, err := item.Data()
		if err != nil {
			return false, err
		}
		if ref != nil && *ref == status.DAGRun() {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) waitForPoll(ctx context.Context) error {
	timer := time.NewTimer(s.PollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func reloadStatus(ctx context.Context, attempt exec.DAGRunAttempt) (*exec.DAGRunStatus, error) {
	latest, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, errorf(ErrorInternal, "failed to reload DAG-run status after waiting for the attempt to settle: %v", err)
	}
	if latest == nil {
		return nil, errorf(ErrorInternal, "failed to reload DAG-run status after waiting for the attempt to settle: status data is nil")
	}
	return latest, nil
}

func attemptFinalizing(status *exec.DAGRunStatus, attemptID, stepID string) (bool, error) {
	if status.Status != core.Waiting || status.AttemptID != attemptID || status.FinishedAt != "" {
		return false, nil
	}
	node, err := findNodeByID(status.Nodes, stepID)
	if err != nil {
		return false, err
	}
	return !nodeCompleted(node), nil
}

func findNodeByID(nodes []*exec.Node, stepID string) (*exec.Node, error) {
	var found *exec.Node
	for _, node := range nodes {
		if node == nil || node.Step.ID != stepID {
			continue
		}
		if found != nil {
			return nil, errorf(ErrorInternal, "human task step ID %q is ambiguous", stepID)
		}
		found = node
	}
	if found == nil {
		return nil, errorf(ErrorNotFound, "human task step ID %q was not found", stepID)
	}
	if found.Step.HumanTask == nil {
		return nil, errorf(ErrorNotFound, "step %q is not a human task", stepID)
	}
	return found, nil
}

func classifyMutationError(prefix string, err error) error {
	var classified *Error
	if errors.As(err, &classified) {
		return err
	}
	return errorf(ErrorInternal, "%s: %v", prefix, err)
}

func nodeCompleted(node *exec.Node) bool {
	return node != nil && len(node.HumanTaskInput) > 0
}

func hasWaitingNodes(nodes []*exec.Node) bool {
	return countWaitingNodes(nodes) > 0
}

func countWaitingNodes(nodes []*exec.Node) int {
	count := 0
	for _, node := range nodes {
		if node != nil && node.Status == core.NodeWaiting {
			count++
		}
	}
	return count
}

func hasCompletedHumanTask(nodes []*exec.Node) bool {
	for _, node := range nodes {
		if node != nil && node.Step.HumanTask != nil && nodeCompleted(node) {
			return true
		}
	}
	return false
}

// HasCompletedTask reports whether status contains durable human-task completion input.
func HasCompletedTask(status *exec.DAGRunStatus) bool {
	return status != nil && hasCompletedHumanTask(status.Nodes)
}

// ResumePending reports whether a run is waiting for a human-task resume handoff.
func ResumePending(status *exec.DAGRunStatus) bool {
	return status != nil && status.Status == core.Waiting && !hasWaitingNodes(status.Nodes) && status.HumanTaskResume != nil
}

// ValidateRetry rejects retry operations that would bypass human-task completion state.
func ValidateRetry(status *exec.DAGRunStatus, stepName, resumeToken string) error {
	if status == nil {
		return nil
	}
	if resumeToken != "" {
		if stepName != "" || !ResumePending(status) || status.HumanTaskResume.ClaimToken != resumeToken {
			return errorf(ErrorConflict, "human-task resume claim is not valid for this DAG-run")
		}
		return nil
	}
	if stepName != "" {
		for _, node := range status.Nodes {
			if node == nil || (node.Step.Name != stepName && node.Step.ID != stepName) {
				continue
			}
			if node.Step.HumanTask != nil {
				return errorf(ErrorConflict, "human task step %q cannot be retried directly", stepName)
			}
			break
		}
	}
	if status.Status == core.Waiting {
		for _, node := range status.Nodes {
			if node != nil && node.Step.HumanTask != nil {
				return errorf(ErrorConflict, "DAG-run %s is waiting on a human-task checkpoint; complete or resume it instead", status.DAGRun())
			}
		}
	}
	return nil
}
