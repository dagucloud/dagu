// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package humantask

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/queue"
)

const (
	defaultSettleTimeout  = 5 * time.Second
	defaultPollInterval   = 50 * time.Millisecond
	defaultEnqueueTimeout = 30 * time.Second
)

var errCompletionAlreadyApplied = errors.New("human task completion already applied")

// ErrorKind classifies failures that callers need to map to their transport.
type ErrorKind string

const (
	ErrorInvalid  ErrorKind = "invalid"
	ErrorNotFound ErrorKind = "not_found"
	ErrorConflict ErrorKind = "conflict"
	ErrorInternal ErrorKind = "internal"
)

// Error is a classified human-task operation failure.
type Error struct {
	Kind ErrorKind
	Err  error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// KindOf returns the classified kind of err.
func KindOf(err error) ErrorKind {
	if target, ok := errors.AsType[*Error](err); ok {
		return target.Kind
	}
	return ErrorInternal
}

func errorf(kind ErrorKind, format string, args ...any) error {
	return &Error{Kind: kind, Err: fmt.Errorf(format, args...)}
}

// ResumeError reports a durable completion whose retry could not be queued.
type ResumeError struct {
	Result Result
	Err    error
}

func (e *ResumeError) Error() string {
	if e.Result.StepID != "" {
		return fmt.Sprintf(
			"human task %q was completed, but the DAG-run could not be queued for resume: %v",
			e.Result.StepID,
			e.Err,
		)
	}
	return fmt.Sprintf("the DAG-run could not be queued for resume: %v", e.Err)
}

func (e *ResumeError) Unwrap() error { return e.Err }

// Service completes human tasks and queues recoverable retries.
type Service struct {
	DAGRunRepository *persis.DAGRunRepository
	QueueStore       queue.QueueStore
	ProcRepository   processRepository
	Now              func() time.Time
	SettleTimeout    time.Duration
	PollInterval     time.Duration
	EnqueueTimeout   time.Duration
}

type processRepository interface {
	IsAttemptAlive(ctx context.Context, groupName string, dagRun ir.DAGRunRef, attemptID string) (bool, error)
}

// CompleteRequest identifies one human task and its typed input.
type CompleteRequest struct {
	DAGName       string
	DAGRunID      string
	StepID        string
	Input         Input
	CompletedBy   string
	CompletedByID string
}

// Result describes the observable outcome of a completion or resume request.
type Result struct {
	DAGName               string
	DAGRunID              string
	StepID                string
	AlreadyCompleted      bool
	Queued                bool
	RemainingWaitingSteps int
}

type target struct {
	dag    *ir.DAG
	status *ir.DAGRunStatus
	ref    ir.DAGRunRef
	stepID string
}

func (s *Service) defaults() {
	if s.Now == nil {
		s.Now = time.Now
	}
	if s.SettleTimeout <= 0 {
		s.SettleTimeout = defaultSettleTimeout
	}
	if s.PollInterval <= 0 {
		s.PollInterval = defaultPollInterval
	}
	if s.EnqueueTimeout <= 0 {
		s.EnqueueTimeout = defaultEnqueueTimeout
	}
}

func (s *Service) loadTarget(ctx context.Context, dagName, dagRunID, stepID string) (*target, error) {
	ref := ir.NewDAGRunRef(dagName, dagRunID)
	attempt, err := s.DAGRunRepository.FindAttempt(ctx, ref)
	if err != nil {
		kind := ErrorInternal
		if errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
			kind = ErrorNotFound
		}
		return nil, errorf(kind, "failed to find DAG-run %q with run ID %q: %v", dagName, dagRunID, err)
	}
	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		return nil, errorf(ErrorInternal, "failed to read DAG from run data: %v", err)
	}
	if dag == nil {
		return nil, errorf(ErrorInternal, "failed to read DAG from run data: DAG data is nil")
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, errorf(ErrorInternal, "failed to read DAG-run status: %v", err)
	}
	if status == nil {
		return nil, errorf(ErrorInternal, "failed to read DAG-run status: status data is nil")
	}
	if stepID != "" {
		status, err = s.waitForCompletionReady(ctx, attempt, dag, status, stepID)
		if err != nil {
			return nil, err
		}
	}
	storedRef := status.DAGRun()
	if storedRef.Zero() {
		return nil, errorf(ErrorInternal, "stored DAG-run identity is incomplete")
	}
	if storedRef != ref {
		return nil, errorf(ErrorInternal, "stored DAG-run identity %s does not match requested DAG-run %s", storedRef, ref)
	}
	return &target{dag: dag, status: status, ref: ref, stepID: stepID}, nil
}

func (t *target) withStatus(status *ir.DAGRunStatus) *target {
	clone := *t
	clone.status = status
	return &clone
}

func resultFor(status *ir.DAGRunStatus, stepID string, alreadyCompleted bool) Result {
	if status == nil {
		return Result{StepID: stepID, AlreadyCompleted: alreadyCompleted}
	}
	return Result{
		DAGName:               status.Name,
		DAGRunID:              status.DAGRunID,
		StepID:                stepID,
		AlreadyCompleted:      alreadyCompleted,
		RemainingWaitingSteps: countWaitingNodes(status.Nodes),
	}
}
