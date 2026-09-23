// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package humantask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	// ExpectedIteration, when set, must equal the task's current push-back
	// iteration, so a request made for an earlier review changes nothing.
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
	// Iteration is the push-back iteration this request recorded.
	Iteration int
	// ResumeRequested reports that the run was ready to resume after the
	// push-back, whether this request queued the resume or a concurrent
	// request queued it first.
	ResumeRequested bool
	Queued          bool
}

// PushBackQueueError reports a push-back that was undone because the DAG-run
// could not be queued for resume. The task is still open and the same request
// can be retried.
type PushBackQueueError struct {
	Result PushBackResult
	Err    error
}

func (e *PushBackQueueError) Error() string {
	return fmt.Sprintf(
		"push-back of human task %q was not applied because the DAG-run could not be queued for resume: %v",
		e.Result.StepID,
		e.Err,
	)
}

func (e *PushBackQueueError) Unwrap() error { return e.Err }

// PushBack validates feedback and resets the task's rewind target and every
// step depending on it for another execution, then queues the run when the
// rewind target can run.
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
	if target.status.Status != ir.Waiting {
		return PushBackResult{}, errorf(
			ErrorConflict,
			"DAG-run %s is not waiting (status: %s)",
			target.ref,
			target.status.Status,
		)
	}

	at := s.Now().UTC().Format(time.RFC3339)
	var original *ir.DAGRunStatus
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
			if request.ExpectedIteration != nil && *request.ExpectedIteration != latestNode.ApprovalIteration {
				return errorf(
					ErrorConflict,
					"human task step %q is at push-back iteration %d, not %d",
					request.StepID,
					latestNode.ApprovalIteration,
					*request.ExpectedIteration,
				)
			}
			original, err = cloneStatus(latest)
			if err != nil {
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
	return s.enqueuePushBackResume(ctx, target.withStatus(updated), original, result)
}

// enqueuePushBackResume queues the resume a push-back needs and undoes the
// push-back when queueing fails. Once the task is reset, repeating the request
// cannot retry the resume, so a push-back is applied only together with it.
func (s *Service) enqueuePushBackResume(
	ctx context.Context,
	target *target,
	original *ir.DAGRunStatus,
	result PushBackResult,
) (PushBackResult, error) {
	if !pushBackResumeReady(target.status.Nodes, result.RewindTo) {
		return result, nil
	}
	result.ResumeRequested = true

	postCommitCtx := context.WithoutCancel(ctx)
	if s.QueueStore == nil {
		return result, s.undoPushBack(postCommitCtx, target, original, result, errors.New("queue store is not configured"))
	}
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
	if errors.Is(err, queue.ErrRetryStaleLatest) {
		// The run left the checkpoint after the push-back was stored, so the
		// attempt that moved it on carries the reset steps.
		return s.verifyPushBackCarried(postCommitCtx, target, result, err)
	}
	return result, s.undoPushBack(postCommitCtx, target, original, result, err)
}

func (s *Service) verifyPushBackCarried(
	ctx context.Context,
	target *target,
	result PushBackResult,
	cause error,
) (PushBackResult, error) {
	readCtx, cancel := context.WithTimeout(ctx, s.EnqueueTimeout)
	defer cancel()
	attempt, err := s.DAGRunRepository.FindAttempt(readCtx, target.ref)
	if err != nil {
		return result, errorf(ErrorInternal, "failed to verify DAG-run status after queue failure: %v", err)
	}
	latest, err := attempt.ReadStatus(readCtx)
	if err != nil {
		return result, errorf(ErrorInternal, "failed to verify DAG-run status after queue failure: %v", err)
	}
	if latest != nil {
		if node, findErr := findNodeByID(latest.Nodes, result.StepID); findErr == nil && node.ApprovalIteration >= result.Iteration {
			return result, nil
		}
	}
	return result, errorf(ErrorInternal, "failed to queue DAG-run resume: %v", cause)
}

func (s *Service) undoPushBack(
	ctx context.Context,
	target *target,
	original *ir.DAGRunStatus,
	result PushBackResult,
	cause error,
) error {
	undoCtx, cancel := context.WithTimeout(ctx, s.EnqueueTimeout)
	defer cancel()
	// Every status is compared in its stored form so that representation
	// differences, such as compacted form JSON, never count as a change.
	applied, err := cloneStatus(target.status)
	swapped := false
	if err == nil {
		_, swapped, err = s.DAGRunRepository.CompareAndSwapLatestAttemptStatus(
			undoCtx,
			target.ref,
			applied.AttemptID,
			ir.Waiting,
			func(latest *ir.DAGRunStatus) error {
				stored, err := cloneStatus(latest)
				if err != nil {
					return err
				}
				if err := dagrun.RevertPushBack(stored, original, applied); err != nil {
					return err
				}
				latest.Nodes = stored.Nodes
				return nil
			}, persis.DAGRunCompareAndSwapOptions{},
		)
	}
	if err == nil && !swapped {
		err = errors.New("the DAG-run changed after the push-back")
	}
	if err != nil {
		return errorf(
			ErrorInternal,
			"push-back of human task %q could not be undone after the DAG-run could not be queued for resume (%v): %v",
			result.StepID,
			cause,
			err,
		)
	}
	return &PushBackQueueError{Result: result, Err: cause}
}

// pushBackResumeReady reports whether a resume after a push-back can make
// progress: no manual step is waiting, or the rewind target can run in a run
// that has nothing for the resume to re-run. A target with build inputs is
// excluded because its inferred producer edges are not stored with the run.
func pushBackResumeReady(nodes []*ir.Node, rewindTo string) bool {
	if !hasWaitingNodes(nodes) {
		return true
	}
	if hasRetryableNode(nodes) {
		return false
	}
	byName := make(map[string]*ir.Node, len(nodes))
	for _, node := range nodes {
		if node != nil {
			byName[node.Step.Name] = node
		}
	}
	target := byName[rewindTo]
	if target == nil || len(target.Step.Inputs) > 0 {
		return false
	}
	for _, name := range target.Step.Depends {
		if dep := byName[name]; dep == nil || !dependencyAllowsRun(dep) {
			return false
		}
	}
	return true
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

func cloneStatus(status *ir.DAGRunStatus) (*ir.DAGRunStatus, error) {
	data, err := json.Marshal(status)
	if err != nil {
		return nil, fmt.Errorf("snapshot DAG-run status: %w", err)
	}
	var clone ir.DAGRunStatus
	if err := json.Unmarshal(data, &clone); err != nil {
		return nil, fmt.Errorf("snapshot DAG-run status: %w", err)
	}
	return &clone, nil
}
