// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package history

import (
	"context"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

// Config provides the stores and path roots required by the History service.
type Config struct {
	DAGRunStore exec.DAGRunStore
	QueueStore  exec.QueueStore
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

	QueueName string

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
	QueueName   string
	LogFile     string
	ArtifactDir string

	StatusCloseErr error
}

// SubmitRun records a new DAG-run lifecycle and publishes its dispatch intent.
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
	DAG     *core.DAG
	Status  *exec.DAGRunStatus
	Options exec.EnqueueRetryOptions
}

// RetryRun persists retry state and publishes the dispatch intent.
func (s *Service) RetryRun(ctx context.Context, cmd RetryRunCommand) error {
	return s.retryRun(ctx, cmd)
}

// CancelQueuedRunCommand cancels a DAG run that has not started execution.
type CancelQueuedRunCommand struct {
	DAGRun exec.DAGRunRef
}

// CancelQueuedRun cancels the latest queued attempt for a DAG run.
func (s *Service) CancelQueuedRun(ctx context.Context, cmd CancelQueuedRunCommand) error {
	return s.cancelQueuedRun(ctx, cmd)
}
