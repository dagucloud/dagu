// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package history

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/logpath"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime/transform"
)

func (s *Service) submitRun(ctx context.Context, cmd SubmitRunCommand) (*SubmittedRun, error) {
	if err := s.validateSubmitRun(cmd); err != nil {
		return nil, err
	}

	now := s.now()
	dagRun := exec.NewDAGRunRef(cmd.DAG.Name, cmd.DAGRunID)
	queueName := submitQueueName(cmd)

	logFile, err := logpath.Generate(ctx, s.cfg.LogBaseDir, cmd.DAG.LogDir, cmd.DAG.Name, cmd.DAGRunID)
	if err != nil {
		return nil, fmt.Errorf("failed to generate log file name: %w", err)
	}

	artifactDir, err := s.submitArtifactDir(ctx, cmd)
	if err != nil {
		return nil, err
	}

	attempt, err := s.cfg.DAGRunStore.CreateAttempt(ctx, cmd.DAG, now, cmd.DAGRunID, cmd.AttemptOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create queued DAG run: %w", err)
	}

	committed := false
	defer func() {
		if committed {
			return
		}
		if rmErr := s.cfg.DAGRunStore.RemoveDAGRun(context.WithoutCancel(ctx), dagRun); rmErr != nil {
			logger.Error(ctx, "Failed to rollback queued DAG run",
				tag.DAG(cmd.DAG.Name),
				tag.RunID(cmd.DAGRunID),
				tag.Error(rmErr),
			)
		}
	}()

	status := queuedStatus(cmd, dagRun, attempt.ID(), logFile, artifactDir, now)
	writeResult, err := writeQueuedStatus(ctx, attempt, status, cmd.ProceedOnStatusCloseErr)
	if err != nil {
		return nil, err
	}

	if err := s.cfg.Scheduler.ScheduleRun(ctx, ScheduleRequest{
		QueueName: queueName,
		Priority:  exec.QueuePriorityLow,
		DAGRun:    dagRun,
	}); err != nil {
		return nil, joinCloseAndEnqueue(
			wrapCloseErr(writeResult.closeErr),
			fmt.Errorf("failed to enqueue DAG run: %w", err),
		)
	}
	committed = true

	return &SubmittedRun{
		DAGRun:         dagRun,
		Attempt:        attempt,
		Status:         status,
		QueueName:      queueName,
		LogFile:        logFile,
		ArtifactDir:    artifactDir,
		StatusCloseErr: writeResult.closeErr,
	}, nil
}

func (s *Service) validateSubmitRun(cmd SubmitRunCommand) error {
	if s.cfg.DAGRunStore == nil {
		return fmt.Errorf("dag-run store is required")
	}
	if s.cfg.Scheduler == nil {
		return fmt.Errorf("scheduler is required")
	}
	if cmd.DAG == nil {
		return fmt.Errorf("dag is required")
	}
	if cmd.DAGRunID == "" {
		return fmt.Errorf("dag-run ID is required")
	}
	return nil
}

func (s *Service) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now()
	}
	return time.Now()
}

func submitQueueName(cmd SubmitRunCommand) string {
	if cmd.QueueName != "" {
		return cmd.QueueName
	}
	return cmd.DAG.ProcGroup()
}

func (s *Service) submitArtifactDir(ctx context.Context, cmd SubmitRunCommand) (string, error) {
	if !cmd.DAG.ArtifactsEnabled() {
		return "", nil
	}

	dir, err := logpath.GenerateDir(ctx, s.cfg.ArtifactBaseDir, cmd.DAG.Artifacts.Dir, cmd.DAG.Name, cmd.DAGRunID)
	if err != nil {
		return "", fmt.Errorf("failed to generate artifact directory: %w", err)
	}
	return dir, nil
}

func queuedStatus(cmd SubmitRunCommand, dagRun exec.DAGRunRef, attemptID, logFile, archiveDir string, now time.Time) exec.DAGRunStatus {
	root := cmd.Root
	if root.Zero() {
		root = dagRun
	}

	opts := []transform.StatusOption{
		transform.WithLogFilePath(logFile),
		transform.WithArchiveDir(archiveDir),
		transform.WithAttemptID(attemptID),
		transform.WithPreconditions(cmd.DAG.Preconditions),
		transform.WithQueuedAt(stringutil.FormatTime(now)),
		transform.WithHierarchyRefs(root, cmd.Parent),
		transform.WithTriggerType(cmd.TriggerType),
		transform.WithRuntimeProfile(cmd.ProfileName, "", nil),
	}
	if cmd.ScheduleTime != "" {
		opts = append(opts, transform.WithScheduleTime(cmd.ScheduleTime))
	}

	return transform.NewStatusBuilder(cmd.DAG).Create(cmd.DAGRunID, core.Queued, 0, time.Time{}, opts...)
}

type queuedStatusWriteResult struct {
	closeErr error
}

func writeQueuedStatus(ctx context.Context, attempt exec.DAGRunAttempt, status exec.DAGRunStatus, proceedOnCloseErr bool) (queuedStatusWriteResult, error) {
	if err := attempt.Open(ctx); err != nil {
		return queuedStatusWriteResult{}, fmt.Errorf("failed to open queued DAG run: %w", err)
	}
	if err := attempt.Write(ctx, status); err != nil {
		_ = attempt.Close(ctx)
		return queuedStatusWriteResult{}, fmt.Errorf("failed to save queued DAG run status: %w", err)
	}
	if err := attempt.Close(ctx); err != nil {
		if proceedOnCloseErr {
			return queuedStatusWriteResult{closeErr: err}, nil
		}
		return queuedStatusWriteResult{}, fmt.Errorf("failed to close queued DAG run: %w", err)
	}
	return queuedStatusWriteResult{}, nil
}

func wrapCloseErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("failed to close queued DAG run: %w", err)
}

func joinCloseAndEnqueue(closeErr, enqueueErr error) error {
	if closeErr == nil {
		return enqueueErr
	}
	return errors.Join(closeErr, enqueueErr)
}
