// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package history

import (
	"context"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

// Config provides the stores and path roots required by the History service.
type Config struct {
	DAGRunStore exec.DAGRunStore
	ProcStore   LocalProcStore

	LogBaseDir      string
	ArtifactBaseDir string

	Now func() time.Time
}

// Service is the DAG-run lifecycle boundary.
type Service struct {
	cfg Config
}

// New creates a History service using the provided dependencies.
func New(cfg Config) *Service {
	return &Service{cfg: cfg}
}

// SubmitRunCommand creates a DAG run that is eligible for dispatch.
type SubmitRunCommand struct {
	DAG      *core.DAG
	DAGRunID string

	Root        exec.DAGRunRef
	Parent      exec.DAGRunRef
	TriggerType core.TriggerType

	ScheduleTime string
	ProfileName  string

	AttemptOptions exec.NewDAGRunAttemptOptions

	ProceedOnStatusCloseErr bool
}

// SubmittedRun is the result of a submitted DAG run.
type SubmittedRun struct {
	DAGRun      exec.DAGRunRef
	Attempt     exec.DAGRunAttempt
	Status      exec.DAGRunStatus
	LogFile     string
	ArtifactDir string

	StatusCloseErr error
}

// SubmitRun records a new DAG-run lifecycle.
func (s *Service) SubmitRun(ctx context.Context, cmd SubmitRunCommand) (*SubmittedRun, error) {
	return s.submitRun(ctx, cmd)
}

// LocalAttemptBuilder creates or resolves the attempt a local execution owns.
type LocalAttemptBuilder func(context.Context) (exec.DAGRunAttempt, error)

// PrepareLocalAttemptCommand prepares a local execution attempt.
type PrepareLocalAttemptCommand struct {
	DAG      *core.DAG
	DAGRunID string

	Root        exec.DAGRunRef
	Parent      exec.DAGRunRef
	TriggerType core.TriggerType

	ScheduleTime string
	ProfileName  string

	BuildAttempt LocalAttemptBuilder
}

// PreparedLocalAttempt is a local attempt with acquired process ownership.
type PreparedLocalAttempt struct {
	Attempt exec.DAGRunAttempt
	Proc    exec.ProcHandle
}

// PrepareLocalAttempt prepares an attempt and acquires local execution ownership.
func (s *Service) PrepareLocalAttempt(ctx context.Context, cmd PrepareLocalAttemptCommand) (*PreparedLocalAttempt, error) {
	return s.prepareLocalAttempt(ctx, cmd)
}

// RetryRunCommand requests a lifecycle transition back to dispatch eligibility.
type RetryRunCommand struct {
	Status  *exec.DAGRunStatus
	Options RetryRunOptions
}

// RetriedRun is the result of a retry lifecycle transition.
type RetriedRun struct {
	DAGRun         exec.DAGRunRef
	Status         *exec.DAGRunStatus
	PreviousStatus exec.DAGRunStatus
}

// RetryRun persists retry state.
func (s *Service) RetryRun(ctx context.Context, cmd RetryRunCommand) (*RetriedRun, error) {
	return s.retryRun(ctx, cmd)
}

// UndoRetryRunCommand restores the status captured before RetryRun.
type UndoRetryRunCommand struct {
	DAGRun         exec.DAGRunRef
	QueuedStatus   *exec.DAGRunStatus
	PreviousStatus exec.DAGRunStatus
}

// UndoRetryRun restores the status captured before RetryRun.
func (s *Service) UndoRetryRun(ctx context.Context, cmd UndoRetryRunCommand) error {
	return s.undoRetryRun(ctx, cmd)
}

// MarkDispatchCanceledCommand records cancellation before execution starts.
type MarkDispatchCanceledCommand struct {
	DAGRun exec.DAGRunRef
}

// MarkDispatchCanceled records cancellation before execution starts.
func (s *Service) MarkDispatchCanceled(ctx context.Context, cmd MarkDispatchCanceledCommand) error {
	return s.markDispatchCanceled(ctx, cmd)
}

// DiscardSubmittedRunCommand removes persisted history for a submitted DAG run.
type DiscardSubmittedRunCommand struct {
	DAGRun exec.DAGRunRef
}

// DiscardSubmittedRun removes persisted history for a submitted DAG run.
func (s *Service) DiscardSubmittedRun(ctx context.Context, cmd DiscardSubmittedRunCommand) error {
	if err := s.validateDiscardSubmittedRun(cmd); err != nil {
		return err
	}
	return s.cfg.DAGRunStore.RemoveDAGRun(ctx, cmd.DAGRun)
}

func (s *Service) validateDiscardSubmittedRun(cmd DiscardSubmittedRunCommand) error {
	if s.cfg.DAGRunStore == nil {
		return fmt.Errorf("dag-run store is required")
	}
	if err := validateDAGRunRef(cmd.DAGRun); err != nil {
		return err
	}
	return nil
}

func validateDAGRunRef(dagRun exec.DAGRunRef) error {
	if dagRun.Name == "" || dagRun.ID == "" {
		return fmt.Errorf("dag-run is required")
	}
	return nil
}
