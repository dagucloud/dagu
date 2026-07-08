// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package matching

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/history"
)

// History records DAG-run lifecycle state for Matching decisions.
type History interface {
	SubmitRun(context.Context, history.SubmitRunCommand) (*history.SubmittedRun, error)
	RetryRun(context.Context, history.RetryRunCommand) (*history.RetriedRun, error)
	UndoRetryRun(context.Context, history.UndoRetryRunCommand) error
	MarkDispatchCanceled(context.Context, history.MarkDispatchCanceledCommand) error
	RemoveRun(context.Context, history.RemoveRunCommand) error
}

// Config provides dependencies required by the Matching service.
type Config struct {
	QueueStore exec.QueueStore
	History    History
}

// Service owns DAG-run queue admission and pending-dispatch cancellation.
type Service struct {
	cfg Config
}

// New creates a Matching service.
func New(cfg Config) *Service {
	return &Service{cfg: cfg}
}

// SubmitRunCommand records a pending run and enqueues it for dispatch.
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

// SubmittedRun is a run admitted into Matching.
type SubmittedRun struct {
	*history.SubmittedRun
	QueueName string
}

// SubmitRun records the run through History and enqueues the dispatch item.
func (s *Service) SubmitRun(ctx context.Context, cmd SubmitRunCommand) (*SubmittedRun, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if cmd.DAG == nil {
		return nil, fmt.Errorf("dag is required")
	}

	submitted, err := s.cfg.History.SubmitRun(ctx, history.SubmitRunCommand{
		DAG:                     cmd.DAG,
		DAGRunID:                cmd.DAGRunID,
		Root:                    cmd.Root,
		Parent:                  cmd.Parent,
		TriggerType:             cmd.TriggerType,
		ScheduleTime:            cmd.ScheduleTime,
		ProfileName:             cmd.ProfileName,
		AttemptOptions:          cmd.AttemptOptions,
		ProceedOnStatusCloseErr: cmd.ProceedOnStatusCloseErr,
	})
	if err != nil {
		return nil, err
	}

	queueName := cmd.QueueName
	if queueName == "" {
		queueName = cmd.DAG.ProcGroup()
	}
	if err := s.cfg.QueueStore.Enqueue(ctx, queueName, exec.QueuePriorityLow, submitted.DAGRun); err != nil {
		removeErr := s.cfg.History.RemoveRun(context.WithoutCancel(ctx), history.RemoveRunCommand{DAGRun: submitted.DAGRun})
		return nil, joinErrors(
			wrapCloseErr(submitted.StatusCloseErr),
			fmt.Errorf("failed to enqueue DAG run: %w", err),
			wrapRollbackErr(removeErr),
		)
	}

	return &SubmittedRun{
		SubmittedRun: submitted,
		QueueName:    queueName,
	}, nil
}

// RetryRunCommand records a retry transition and enqueues it for dispatch.
type RetryRunCommand struct {
	DAG       *core.DAG
	Status    *exec.DAGRunStatus
	Options   history.RetryRunOptions
	QueueName string
}

// RetryRun records a retry through History and enqueues the dispatch item.
func (s *Service) RetryRun(ctx context.Context, cmd RetryRunCommand) (*history.RetriedRun, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if cmd.Status == nil {
		return nil, fmt.Errorf("status is required")
	}
	if cmd.Status.Status == core.Queued {
		return s.cfg.History.RetryRun(ctx, history.RetryRunCommand{
			DAG:     cmd.DAG,
			Status:  cmd.Status,
			Options: cmd.Options,
		})
	}

	queueName := retryQueueName(cmd)
	if queueName == "" {
		return nil, errors.New("enqueue retry: proc group is empty")
	}

	retried, err := s.cfg.History.RetryRun(ctx, history.RetryRunCommand{
		DAG:     cmd.DAG,
		Status:  cmd.Status,
		Options: cmd.Options,
	})
	if err != nil {
		return nil, err
	}

	if err := s.cfg.QueueStore.Enqueue(ctx, queueName, exec.QueuePriorityLow, retried.DAGRun); err != nil {
		undoErr := s.cfg.History.UndoRetryRun(context.WithoutCancel(ctx), history.UndoRetryRunCommand{
			DAGRun:         retried.DAGRun,
			QueuedStatus:   retried.Status,
			PreviousStatus: retried.PreviousStatus,
		})
		return nil, joinErrors(fmt.Errorf("enqueue retry: %w", err), wrapRollbackErr(undoErr))
	}

	return retried, nil
}

// CancelPendingRunCommand removes pending dispatch and records cancellation.
type CancelPendingRunCommand struct {
	DAGRun                 exec.DAGRunRef
	QueueName              string
	ItemIDs                []string
	IgnoreMissingQueueItem bool
}

// CancelPendingRun cancels a run that has not started execution.
func (s *Service) CancelPendingRun(ctx context.Context, cmd CancelPendingRunCommand) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.cfg.History.MarkDispatchCanceled(ctx, history.MarkDispatchCanceledCommand{DAGRun: cmd.DAGRun}); err != nil {
		return err
	}

	if len(cmd.ItemIDs) > 0 {
		_, err := s.cfg.QueueStore.DeleteByItemIDs(ctx, cmd.QueueName, cmd.ItemIDs)
		if cmd.IgnoreMissingQueueItem && errors.Is(err, exec.ErrQueueItemNotFound) {
			return nil
		}
		return err
	}

	_, err := s.cfg.QueueStore.DequeueByDAGRunID(ctx, cmd.QueueName, cmd.DAGRun)
	if cmd.IgnoreMissingQueueItem && errors.Is(err, exec.ErrQueueItemNotFound) {
		return nil
	}
	return err
}

func (s *Service) validate() error {
	if s.cfg.History == nil {
		return fmt.Errorf("history is required")
	}
	if s.cfg.QueueStore == nil {
		return fmt.Errorf("queue store is required")
	}
	return nil
}

func retryQueueName(cmd RetryRunCommand) string {
	if cmd.QueueName != "" {
		return cmd.QueueName
	}
	if cmd.Status != nil && cmd.Status.ProcGroup != "" {
		return cmd.Status.ProcGroup
	}
	if cmd.DAG != nil {
		return cmd.DAG.ProcGroup()
	}
	if cmd.Status != nil {
		return cmd.Status.Name
	}
	return ""
}

func wrapCloseErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("failed to close queued DAG run: %w", err)
}

func wrapRollbackErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("rollback matching admission: %w", err)
}

func joinErrors(errs ...error) error {
	var nonNil []error
	for _, err := range errs {
		if err != nil {
			nonNil = append(nonNil, err)
		}
	}
	return errors.Join(nonNil...)
}
