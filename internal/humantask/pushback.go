// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package humantask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

// PushBackRequest identifies one human task and the feedback that sends it
// back to its rewind target.
type PushBackRequest struct {
	DAGName  string
	DAGRunID string
	StepID   string
	Input    Input
	// ExpectedIteration, when set, must equal the task's push-back iteration
	// when the request was made, so a request made for an earlier review
	// changes nothing.
	ExpectedIteration *int
	By                string
	ByID              string
}

// PushBackResult describes the observable outcome of a push-back.
type PushBackResult struct {
	DAGName  string
	DAGRunID string
	StepID   string
	// RewindTo is the name of the step that runs again first.
	RewindTo string
	// Iteration is the push-back iteration the push-back recorded.
	Iteration int
	// AlreadyPushedBack reports that identical feedback had already pushed
	// the task back and the task has not opened again since.
	AlreadyPushedBack bool
	// ResumeRequested reports that the run was ready to resume after the
	// push-back, whether this request queued the resume or a concurrent
	// request queued it first.
	ResumeRequested bool
	Queued          bool
}

// PushBackQueueError reports a stored push-back whose DAG-run could not be
// queued for resume. Repeating the same push-back or resuming the run retries
// the queue without changing the push-back.
type PushBackQueueError struct {
	Result PushBackResult
	Err    error
}

func (e *PushBackQueueError) Error() string {
	return fmt.Sprintf(
		"human task %q was pushed back, but the DAG-run could not be queued for resume: %v",
		e.Result.StepID,
		e.Err,
	)
}

func (e *PushBackQueueError) Unwrap() error { return e.Err }

// PushBack validates feedback and resets the task's rewind target and every
// step depending on it for another execution, then queues the run when the
// rewind target can run. The push-back is stored before the queue is
// requested; an identical repeat before the task opens again only retries the
// queue.
func (s *Service) PushBack(ctx context.Context, request PushBackRequest) (PushBackResult, error) {
	s.defaults()
	if s.DAGRunRepository == nil {
		return PushBackResult{}, errorf(ErrorInternal, "DAG-run repository is not configured")
	}
	request.StepID = strings.TrimSpace(request.StepID)
	if request.StepID == "" {
		return PushBackResult{}, errorf(ErrorInvalid, "human task step ID must not be empty")
	}
	if request.ExpectedIteration != nil && *request.ExpectedIteration < 0 {
		return PushBackResult{}, errorf(ErrorInvalid, "expected push-back iteration must not be negative")
	}

	target, err := s.loadTarget(ctx, request.DAGName, request.DAGRunID, request.StepID)
	if err != nil {
		return PushBackResult{}, err
	}
	node, err := findNodeByID(target.status.Nodes, request.StepID)
	if err != nil {
		return PushBackResult{}, err
	}
	config := node.Step.HumanTask.PushBack
	if config == nil {
		return PushBackResult{}, errorf(ErrorInvalid, "human task step %q does not declare with.push_back", request.StepID)
	}
	feedback, allowed, err := preparePushBack(target.dag, node, config, request.Input)
	if err != nil {
		return PushBackResult{}, err
	}
	if nodeCompleted(node) {
		return PushBackResult{}, errorf(ErrorConflict, "human task step %q was already completed", request.StepID)
	}
	if ownPushBackPending(node) {
		return s.repeatPushBack(ctx, target, node, config, feedback, request)
	}
	if target.status.Status != ir.Waiting {
		return PushBackResult{}, errorf(
			ErrorConflict,
			"DAG-run %s is not waiting (status: %s)",
			target.ref,
			target.status.Status,
		)
	}

	at := s.Now().UTC().Format(time.RFC3339)
	var iteration int
	updated, swapped, err := s.DAGRunRepository.CompareAndSwapLatestAttemptStatus(
		ctx,
		target.ref,
		target.status.AttemptID,
		ir.Waiting,
		func(latest *ir.DAGRunStatus) error {
			latestNode, err := findNodeByID(latest.Nodes, request.StepID)
			if err != nil {
				return err
			}
			if nodeCompleted(latestNode) {
				return errorf(ErrorConflict, "human task step %q was already completed", request.StepID)
			}
			if latestNode.Status != ir.NodeWaiting {
				return errorf(
					ErrorConflict,
					"human task step %q is not waiting (status: %s)",
					request.StepID,
					latestNode.Status,
				)
			}
			if err := checkExpectedIteration(request, latestNode.ApprovalIteration); err != nil {
				return err
			}
			iteration, err = dagrun.ApplyPushBack(latest, latestNode, dagrun.PushBack{
				TargetName:    config.RewindTo,
				AllowedInputs: allowed,
				Inputs:        feedback,
				By:            request.By,
				ByID:          request.ByID,
				At:            at,
			})
			return err
		}, persis.DAGRunCompareAndSwapOptions{},
	)
	if err != nil {
		return PushBackResult{}, classifyMutationError("failed to push back human task", err)
	}
	if !swapped {
		return PushBackResult{}, errorf(
			ErrorConflict,
			"DAG-run changed while pushing back human task %q; inspect its current status and retry",
			request.StepID,
		)
	}

	result := PushBackResult{
		DAGName:   updated.Name,
		DAGRunID:  updated.DAGRunID,
		StepID:    request.StepID,
		RewindTo:  config.RewindTo,
		Iteration: iteration,
	}
	return s.enqueuePushBackResume(ctx, target.withStatus(updated), result)
}

// repeatPushBack answers a push-back request for a task whose own push-back
// has not run yet. Identical feedback only retries the queue, like a repeated
// completion; different feedback conflicts with the stored push-back.
func (s *Service) repeatPushBack(
	ctx context.Context,
	target *target,
	node *ir.Node,
	config *ir.HumanTaskPushBackConfig,
	feedback map[string]string,
	request PushBackRequest,
) (PushBackResult, error) {
	stored := node.PushBackHistory[len(node.PushBackHistory)-1]
	if request.ExpectedIteration != nil && *request.ExpectedIteration != pushBackStartIteration(node) {
		return PushBackResult{}, errorf(
			ErrorConflict,
			"human task step %q was already pushed back at iteration %d and has not opened again",
			request.StepID,
			node.ApprovalIteration,
		)
	}
	if !maps.Equal(stored.Inputs, feedback) {
		return PushBackResult{}, errorf(
			ErrorConflict,
			"human task step %q was already pushed back with different feedback",
			request.StepID,
		)
	}
	result := PushBackResult{
		DAGName:           target.status.Name,
		DAGRunID:          target.status.DAGRunID,
		StepID:            request.StepID,
		RewindTo:          config.RewindTo,
		Iteration:         node.ApprovalIteration,
		AlreadyPushedBack: true,
	}
	return s.enqueuePushBackResume(ctx, target, result)
}

// checkExpectedIteration rejects a request made for a review other than the
// one at iteration.
func checkExpectedIteration(request PushBackRequest, iteration int) error {
	if request.ExpectedIteration == nil || *request.ExpectedIteration == iteration {
		return nil
	}
	return errorf(
		ErrorConflict,
		"human task step %q is at push-back iteration %d, not %d",
		request.StepID,
		iteration,
		*request.ExpectedIteration,
	)
}

// pushBackStartIteration returns the iteration a pending push-back started
// from. Every reset stamps a step's iteration on its latest history entry, so
// the entry before the pending one holds it; a push-back can skip iterations
// when a reset step was already ahead.
func pushBackStartIteration(node *ir.Node) int {
	if n := len(node.PushBackHistory); n > 1 {
		return node.PushBackHistory[n-2].Iteration
	}
	return 0
}

// ownPushBackPending reports whether node's own push-back is stored and the
// task has not opened again since.
func ownPushBackPending(node *ir.Node) bool {
	return pushBackPending(node) && node.PushBackHistory[len(node.PushBackHistory)-1].Step == node.Step.Name
}

// enqueuePushBackResume queues the resume a stored push-back needs. A queue
// failure leaves the push-back stored and the resume pending.
func (s *Service) enqueuePushBackResume(ctx context.Context, target *target, result PushBackResult) (PushBackResult, error) {
	if target.status.Status != ir.Waiting || !pushBackResumeReady(target.status.Nodes) {
		return result, nil
	}
	result.ResumeRequested = true
	if s.QueueStore == nil {
		return result, &PushBackQueueError{Result: result, Err: errors.New("queue store is not configured")}
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
	if err == nil {
		result.Queued = queued
		return result, nil
	}

	readCtx, readCancel := context.WithTimeout(postCommitCtx, s.EnqueueTimeout)
	defer readCancel()
	latest, readErr := s.readLatestStatus(readCtx, target.ref)
	if readErr != nil {
		return result, errorf(ErrorInternal, "failed to verify DAG-run status after queue failure: %v", readErr)
	}
	if ResumePending(latest) {
		return result, &PushBackQueueError{Result: result, Err: err}
	}
	if errors.Is(err, queue.ErrRetryStaleLatest) {
		return pushBackCarried(target, latest, result, err)
	}
	return result, errorf(ErrorInternal, "failed to queue DAG-run resume: %v", err)
}

// pushBackCarried reports the outcome when the run left the checkpoint that
// stored the push-back before this request could queue it.
func pushBackCarried(target *target, latest *ir.DAGRunStatus, result PushBackResult, cause error) (PushBackResult, error) {
	node, err := findNodeByID(latest.Nodes, result.StepID)
	if err != nil || node.ApprovalIteration < result.Iteration {
		return result, errorf(ErrorInternal, "failed to queue DAG-run resume: %v", cause)
	}
	if latest.AttemptID != target.status.AttemptID || latest.Status == ir.Queued || latest.Status == ir.Running {
		return result, nil
	}
	return result, errorf(
		ErrorConflict,
		"human task %q was pushed back, but the DAG-run left waiting (status: %s) before it could be queued for resume; retry the DAG-run to run the reset steps",
		result.StepID,
		latest.Status,
	)
}

func (s *Service) readLatestStatus(ctx context.Context, ref ir.DAGRunRef) (*ir.DAGRunStatus, error) {
	attempt, err := s.DAGRunRepository.FindAttempt(ctx, ref)
	if err != nil {
		return nil, err
	}
	latest, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, err
	}
	if latest == nil {
		return nil, dagrun.ErrNoStatusData
	}
	return latest, nil
}

// preparePushBack validates feedback against the push-back form and returns
// the feedback values with the declared feedback property names.
func preparePushBack(
	dag *ir.DAG,
	node *ir.Node,
	config *ir.HumanTaskPushBackConfig,
	input Input,
) (map[string]string, []string, error) {
	result, err := spec.ValidateHumanTaskInputs(config.Form, input.Values, input.CoerceStrings)
	if err != nil {
		return nil, nil, errorf(ErrorInvalid, "invalid feedback for human task step %q: %v", node.Step.ID, err)
	}
	if _, err := marshalOutputs(dag, result); err != nil {
		return nil, nil, errorf(ErrorInvalid, "human task step %q: %v", node.Step.ID, err)
	}
	if err := dagrun.ValidatePushBackInputsSize(result.Outputs); err != nil {
		return nil, nil, errorf(ErrorInvalid, "human task step %q: %v", node.Step.ID, err)
	}
	allowed, err := formPropertyNames(config.Form)
	if err != nil {
		return nil, nil, errorf(ErrorInternal, "human task step %q: %v", node.Step.ID, err)
	}
	return result.Outputs, allowed, nil
}

func formPropertyNames(form json.RawMessage) ([]string, error) {
	if len(form) == 0 {
		return nil, nil
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(form, &schema); err != nil {
		return nil, fmt.Errorf("parse stored push-back form: %w", err)
	}
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}
