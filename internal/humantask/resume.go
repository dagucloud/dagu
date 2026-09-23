// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package humantask

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/queue"
)

// Resume retries a pending human-task enqueue without requiring the submitted form values.
func (s *Service) Resume(ctx context.Context, dagName, dagRunID string) (Result, error) {
	s.defaults()
	if s.DAGRunRepository == nil {
		return Result{}, errorf(ErrorInternal, "DAG-run repository is not configured")
	}
	target, err := s.loadTarget(ctx, dagName, dagRunID, "")
	if err != nil {
		return Result{}, err
	}
	nodes := target.status.Nodes
	if target.status.Status != ir.Waiting {
		if !hasCompletedHumanTask(nodes) && !hasPendingPushBack(nodes) {
			return Result{}, errorf(ErrorConflict, "DAG-run %s has no completed human-task checkpoint to resume", target.ref)
		}
		return resultFor(target.status, "", true), nil
	}
	if !pushBackResumeReady(nodes) {
		if !resumeReady(nodes) {
			return Result{}, errorf(ErrorConflict, "DAG-run %s has no step ready to run while manual steps wait for input", target.ref)
		}
		if !hasCompletedHumanTask(nodes) {
			return Result{}, errorf(ErrorConflict, "DAG-run %s has no completed human-task checkpoint to resume", target.ref)
		}
	}
	result := resultFor(target.status, "", true)
	return s.enqueueResume(ctx, target, result)
}

func (s *Service) enqueueResume(ctx context.Context, target *target, result Result) (Result, error) {
	if target.status == nil || target.status.Status != ir.Waiting ||
		(!resumeReady(target.status.Nodes) && !pushBackResumeReady(target.status.Nodes)) {
		return result, nil
	}
	result.ResumeRequested = true
	if s.QueueStore == nil {
		return result, &ResumeError{Result: result, Err: errors.New("queue store is not configured")}
	}

	postCommitCtx := context.WithoutCancel(ctx)
	enqueueCtx, cancel := context.WithTimeout(postCommitCtx, s.EnqueueTimeout)
	defer cancel()
	queued, err := queue.EnqueueRetry(
		enqueueCtx,
		s.DAGRunRepository,
		s.QueueStore,
		target.dag,
		target.status,
		queue.EnqueueRetryOptions{},
	)
	if err != nil {
		var latest *ir.DAGRunStatus
		readCtx, readCancel := context.WithTimeout(postCommitCtx, s.EnqueueTimeout)
		defer readCancel()
		attempt, readErr := s.DAGRunRepository.FindAttempt(readCtx, target.ref)
		if readErr == nil {
			latest, readErr = attempt.ReadStatus(readCtx)
			if readErr == nil && latest != nil && ResumePending(latest) {
				return result, &ResumeError{Result: result, Err: err}
			}
			if readErr == nil && latest == nil {
				readErr = dagrun.ErrNoStatusData
			}
		}
		if readErr != nil {
			return result, errorf(ErrorInternal, "failed to verify DAG-run status after queue failure: %v", readErr)
		}
		if errors.Is(err, queue.ErrRetryStaleLatest) {
			completed := hasCompletedHumanTask(latest.Nodes) || latest.AttemptID != target.status.AttemptID
			if result.StepID != "" {
				node, findErr := findNodeByID(latest.Nodes, result.StepID)
				completed = findErr == nil && nodeCompleted(node)
			}
			if completed {
				return result, nil
			}
		}
		return result, errorf(ErrorInternal, "failed to queue DAG-run resume: %v", err)
	}
	result.Queued = queued
	return result, nil
}

func (s *Service) waitForCompletionReady(
	ctx context.Context,
	attempt dagrun.Attempt,
	dag *ir.DAG,
	status *ir.DAGRunStatus,
	stepID string,
) (*ir.DAGRunStatus, error) {
	if status.Status != ir.Waiting || status.AttemptID == "" {
		return status, nil
	}
	originalAttemptID := status.AttemptID
	if !dispatch.IsRemoteWorkerID(status.WorkerID) && s.ProcRepository != nil {
		deadline := s.Now().Add(s.SettleTimeout)
		for {
			alive, err := s.ProcRepository.IsAttemptAlive(ctx, dag.ProcGroup(), status.DAGRun(), status.AttemptID)
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

func reloadStatus(ctx context.Context, attempt dagrun.Attempt) (*ir.DAGRunStatus, error) {
	latest, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, errorf(ErrorInternal, "failed to reload DAG-run status after waiting for the attempt to settle: %v", err)
	}
	if latest == nil {
		return nil, errorf(ErrorInternal, "failed to reload DAG-run status after waiting for the attempt to settle: status data is nil")
	}
	return latest, nil
}

func attemptFinalizing(status *ir.DAGRunStatus, attemptID, stepID string) (bool, error) {
	if status.Status != ir.Waiting || status.AttemptID != attemptID || status.FinishedAt != "" {
		return false, nil
	}
	node, err := findNodeByID(status.Nodes, stepID)
	if err != nil {
		return false, err
	}
	return !nodeCompleted(node), nil
}

func findNodeByID(nodes []*ir.Node, stepID string) (*ir.Node, error) {
	var found *ir.Node
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
	if _, ok := errors.AsType[*Error](err); ok {
		return err
	}
	return errorf(ErrorInternal, "%s: %v", prefix, err)
}

func nodeCompleted(node *ir.Node) bool {
	return node != nil && len(node.HumanTaskInput) > 0
}

func hasWaitingNodes(nodes []*ir.Node) bool {
	return countWaitingNodes(nodes) > 0
}

func countWaitingNodes(nodes []*ir.Node) int {
	count := 0
	for _, node := range nodes {
		if node != nil && node.Status == ir.NodeWaiting {
			count++
		}
	}
	return count
}

// resumeReady reports whether a resumed attempt can make progress: either no
// manual step is waiting, or a completed human task unblocked a step in a run
// that has nothing for the resume to re-run.
func resumeReady(nodes []*ir.Node) bool {
	return !hasWaitingNodes(nodes) || (!hasRetryableNode(nodes) && hasUnblockedNode(nodes))
}

// hasRetryableNode reports whether a node has a status that every resume
// re-runs (see runtime Plan.setupRetry). Such runs resume only once no manual
// step is waiting, so those steps run again once rather than at each resume.
func hasRetryableNode(nodes []*ir.Node) bool {
	for _, node := range nodes {
		if node == nil {
			continue
		}
		switch node.Status {
		case ir.NodeFailed, ir.NodeRetrying, ir.NodeAborted, ir.NodeRejected:
			return true
		case ir.NodeNotStarted, ir.NodeRunning, ir.NodeSucceeded, ir.NodeSkipped,
			ir.NodePartiallySucceeded, ir.NodeWaiting:
		}
	}
	return false
}

// hasUnblockedNode reports whether a not-started step will run once resumed
// because at least one of its dependencies is a completed human task.
func hasUnblockedNode(nodes []*ir.Node) bool {
	byName := nodesByName(nodes)
	for _, node := range nodes {
		if nodeRunnable(node, byName) && dependsOnCompletedHumanTask(node, byName) {
			return true
		}
	}
	return false
}

// pushBackResumeReady reports whether a human-task push-back awaits a resume
// that can make progress: no manual step is waiting, or the push-back's rewind
// target can run in a run that has nothing for the resume to re-run.
func pushBackResumeReady(nodes []*ir.Node) bool {
	byName := nodesByName(nodes)
	pending, runnable := false, false
	for _, node := range nodes {
		if !pushBackPending(node) {
			continue
		}
		pending = true
		runnable = runnable || nodeRunnable(node, byName)
	}
	return pending && (!hasWaitingNodes(nodes) || (!hasRetryableNode(nodes) && runnable))
}

// pushBackPending reports whether a human-task push-back reset node and no
// resumed attempt has run it since.
func pushBackPending(node *ir.Node) bool {
	if node == nil || node.Status != ir.NodeNotStarted {
		return false
	}
	n := len(node.PushBackHistory)
	return n > 0 && node.PushBackHistory[n-1].HumanTask && node.PushBackHistory[n-1].Iteration == node.ApprovalIteration
}

func hasPendingPushBack(nodes []*ir.Node) bool {
	return slices.ContainsFunc(nodes, pushBackPending)
}

// nodeRunnable reports whether a not-started step will run once resumed
// because every dependency lets it proceed. Steps with build inputs are
// excluded because their inferred producer edges are not stored with the run.
func nodeRunnable(node *ir.Node, byName map[string]*ir.Node) bool {
	if node == nil || node.Status != ir.NodeNotStarted || len(node.Step.Inputs) > 0 {
		return false
	}
	for _, name := range node.Step.Depends {
		if dep := byName[name]; dep == nil || !dependencyAllowsRun(dep) {
			return false
		}
	}
	return true
}

func dependsOnCompletedHumanTask(node *ir.Node, byName map[string]*ir.Node) bool {
	for _, name := range node.Step.Depends {
		if dep := byName[name]; dep != nil && dep.Step.HumanTask != nil && nodeCompleted(dep) {
			return true
		}
	}
	return false
}

func nodesByName(nodes []*ir.Node) map[string]*ir.Node {
	byName := make(map[string]*ir.Node, len(nodes))
	for _, node := range nodes {
		if node != nil {
			byName[node.Step.Name] = node
		}
	}
	return byName
}

// dependencyAllowsRun mirrors the runtime readiness check (runtime isReady) for
// states it can decide from stored status. A failed dependency never counts
// because its continuation can depend on exit codes and logs.
func dependencyAllowsRun(dep *ir.Node) bool {
	switch dep.Status {
	case ir.NodeSucceeded, ir.NodePartiallySucceeded:
		return true
	case ir.NodeSkipped:
		return dep.SkippedByRetry || dep.Step.ContinueOn.Skipped
	case ir.NodeNotStarted, ir.NodeRunning, ir.NodeFailed, ir.NodeAborted,
		ir.NodeWaiting, ir.NodeRejected, ir.NodeRetrying:
		return false
	}
	return false
}

func hasCompletedHumanTask(nodes []*ir.Node) bool {
	for _, node := range nodes {
		if node != nil && node.Step.HumanTask != nil && nodeCompleted(node) {
			return true
		}
	}
	return false
}

func hasWaitingHumanTask(nodes []*ir.Node) bool {
	for _, node := range nodes {
		if node != nil && node.Status == ir.NodeWaiting && node.Step.HumanTask != nil {
			return true
		}
	}
	return false
}

// HasCompletedTask reports whether status contains durable human-task completion input.
func HasCompletedTask(status *ir.DAGRunStatus) bool {
	return status != nil && hasCompletedHumanTask(status.Nodes)
}

// ResumePending reports whether a run is waiting for its human-task retry to be queued.
func ResumePending(status *ir.DAGRunStatus) bool {
	if status == nil || status.Status != ir.Waiting {
		return false
	}
	return (resumeReady(status.Nodes) && hasCompletedHumanTask(status.Nodes)) || pushBackResumeReady(status.Nodes)
}

// PushBackPending reports whether a stored human-task push-back has not been
// run by a resumed attempt yet.
func PushBackPending(status *ir.DAGRunStatus) bool {
	return status != nil && hasPendingPushBack(status.Nodes)
}

// ValidateRetry rejects retry operations that would bypass human-task completion state.
func ValidateRetry(status *ir.DAGRunStatus, stepName string) error {
	if status == nil {
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
	// Approval and agent-session flows retry runs that still have waiting
	// steps, so only a checkpoint with no waiting step is reserved for resume.
	if status.Status == ir.Waiting && (hasWaitingHumanTask(status.Nodes) ||
		(!hasWaitingNodes(status.Nodes) && hasCompletedHumanTask(status.Nodes))) {
		return errorf(ErrorConflict, "DAG-run %s is waiting on a human-task checkpoint; complete or resume it instead", status.DAGRun())
	}
	return nil
}
