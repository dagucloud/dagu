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
	AttemptID   string
	Status      exec.DAGRunStatus
	LogFile     string
	ArtifactDir string

	RollbackToken  SubmitRollbackToken
	StatusCloseErr error
}

// SubmitRun records a new DAG-run lifecycle.
func (s *Service) SubmitRun(ctx context.Context, cmd SubmitRunCommand) (*SubmittedRun, error) {
	return s.submitRun(ctx, cmd)
}

// PrepareAttemptMode selects how History resolves the execution attempt.
type PrepareAttemptMode string

const (
	PrepareAttemptCreate       PrepareAttemptMode = "create"
	PrepareAttemptOpenExisting PrepareAttemptMode = "open_existing"
	PrepareAttemptOpenSub      PrepareAttemptMode = "open_sub"
)

// PrepareLocalAttemptCommand prepares a local execution attempt.
type PrepareLocalAttemptCommand struct {
	DAG      *core.DAG
	DAGRunID string

	Root        exec.DAGRunRef
	Parent      exec.DAGRunRef
	TriggerType core.TriggerType

	ScheduleTime string
	ProfileName  string

	Mode           PrepareAttemptMode
	AttemptID      string
	AttemptOptions exec.NewDAGRunAttemptOptions
}

// PreparedLocalAttempt is a local attempt with acquired process ownership.
type PreparedLocalAttempt struct {
	Execution *ExecutionContext
	Status    *exec.DAGRunStatus
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
	DAGRun        exec.DAGRunRef
	Status        *exec.DAGRunStatus
	RollbackToken RetryRollbackToken
}

// RetryRun persists retry state.
func (s *Service) RetryRun(ctx context.Context, cmd RetryRunCommand) (*RetriedRun, error) {
	return s.retryRun(ctx, cmd)
}

// UndoRetryRunCommand restores the status captured before RetryRun.
type UndoRetryRunCommand struct {
	RollbackToken RetryRollbackToken
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

// RepairQueuedCatchupRunCommand repairs persisted metadata for a queued catchup run.
type RepairQueuedCatchupRunCommand struct {
	DAG    *core.DAG
	Status *exec.DAGRunStatus
	Root   exec.DAGRunRef
}

// RepairQueuedCatchupRun fills missing local metadata before a queued catchup run executes.
func (s *Service) RepairQueuedCatchupRun(ctx context.Context, cmd RepairQueuedCatchupRunCommand) error {
	return s.repairQueuedCatchupRun(ctx, cmd)
}

// RecordEarlyFailureCommand records a failed lifecycle before execution starts.
type RecordEarlyFailureCommand struct {
	DAG      *core.DAG
	DAGRunID string
	Err      error
}

// RecordEarlyFailure records a failed lifecycle before execution starts.
func (s *Service) RecordEarlyFailure(ctx context.Context, cmd RecordEarlyFailureCommand) error {
	return s.recordEarlyFailure(ctx, cmd)
}

// SeedEditRetryRunCommand creates a queued retry seed for edited DAG execution.
type SeedEditRetryRunCommand struct {
	DAG      *core.DAG
	DAGRunID string

	Params        string
	ProfileName   string
	SourceStatus  *exec.DAGRunStatus
	SkippedSteps  []string
	SourceWorkDir string
}

// SeededEditRetryRun is the result of creating an edit-retry seed.
type SeededEditRetryRun struct {
	DAGRun exec.DAGRunRef
	Status *exec.DAGRunStatus
}

// SeedEditRetryRun records the queued lifecycle state for an edit retry.
func (s *Service) SeedEditRetryRun(ctx context.Context, cmd SeedEditRetryRunCommand) (*SeededEditRetryRun, error) {
	return s.seedEditRetryRun(ctx, cmd)
}

// MarkEditRetrySeedFailedCommand records a failed edit-retry seed.
type MarkEditRetrySeedFailedCommand struct {
	Status *exec.DAGRunStatus
	Cause  error
}

// MarkEditRetrySeedFailed marks an edit-retry seed as failed if it is still queued.
func (s *Service) MarkEditRetrySeedFailed(ctx context.Context, cmd MarkEditRetrySeedFailedCommand) error {
	return s.markEditRetrySeedFailed(ctx, cmd)
}

// DiscardSubmittedRunCommand removes persisted history for a submitted DAG run.
type DiscardSubmittedRunCommand struct {
	RollbackToken SubmitRollbackToken
}

// DiscardSubmittedRun removes persisted history for a submitted DAG run.
func (s *Service) DiscardSubmittedRun(ctx context.Context, cmd DiscardSubmittedRunCommand) error {
	if err := s.validateDiscardSubmittedRun(cmd); err != nil {
		return err
	}
	return s.cfg.DAGRunStore.RemoveDAGRun(ctx, cmd.RollbackToken.dagRun)
}

func (s *Service) validateDiscardSubmittedRun(cmd DiscardSubmittedRunCommand) error {
	if s.cfg.DAGRunStore == nil {
		return fmt.Errorf("dag-run store is required")
	}
	if err := validateDAGRunRef(cmd.RollbackToken.dagRun); err != nil {
		return err
	}
	return nil
}

// SubmitRollbackToken authorizes rollback of a submitted run.
type SubmitRollbackToken struct {
	dagRun exec.DAGRunRef
}

// RetryRollbackToken authorizes rollback of a retry transition.
type RetryRollbackToken struct {
	dagRun         exec.DAGRunRef
	queuedStatus   exec.DAGRunStatus
	previousStatus exec.DAGRunStatus
}

func validateDAGRunRef(dagRun exec.DAGRunRef) error {
	if dagRun.Name == "" || dagRun.ID == "" {
		return fmt.Errorf("dag-run is required")
	}
	return nil
}
