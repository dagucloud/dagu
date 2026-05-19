// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

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

// QueueRequest describes a DAG-run intake operation that persists a queued
// attempt before publishing the queue item.
type QueueRequest struct {
	DAGRunStore exec.DAGRunStore
	QueueStore  exec.QueueStore
	DAG         *core.DAG
	DAGRunID    string

	QueueName string

	LogBaseDir      string
	ArtifactBaseDir string

	Root         exec.DAGRunRef
	Parent       exec.DAGRunRef
	TriggerType  core.TriggerType
	ScheduleTime string

	AttemptOptions exec.NewDAGRunAttemptOptions

	// ProceedOnStatusCloseErr preserves legacy CLI enqueue behavior: publish
	// the queue item after best-effort close so readers can see the queued status.
	ProceedOnStatusCloseErr bool

	Now func() time.Time
}

// QueuedRun is the result of successful DAG-run queue intake.
type QueuedRun struct {
	DAGRun      exec.DAGRunRef
	Attempt     exec.DAGRunAttempt
	Status      exec.DAGRunStatus
	QueueName   string
	LogFile     string
	ArtifactDir string
	// StatusCloseErr is set only when ProceedOnStatusCloseErr allowed intake
	// to continue after the status attempt close failed.
	StatusCloseErr error
}

// EnqueueRun creates a queued DAG-run attempt, writes the queued status, closes
// the attempt, then publishes the queue item. If any post-create step fails, the
// created attempt is rolled back.
func EnqueueRun(ctx context.Context, req QueueRequest) (*QueuedRun, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}

	now := req.now()
	dagRun := exec.NewDAGRunRef(req.DAG.Name, req.DAGRunID)
	queueName := req.queueName()

	logFile, err := logpath.Generate(ctx, req.LogBaseDir, req.DAG.LogDir, req.DAG.Name, req.DAGRunID)
	if err != nil {
		return nil, fmt.Errorf("failed to generate log file name: %w", err)
	}

	artifactDir, err := artifactDir(ctx, req)
	if err != nil {
		return nil, err
	}

	attempt, err := req.DAGRunStore.CreateAttempt(ctx, req.DAG, now, req.DAGRunID, req.AttemptOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create queued DAG run: %w", err)
	}

	committed := false
	defer func() {
		if committed {
			return
		}
		if rmErr := req.DAGRunStore.RemoveDAGRun(context.WithoutCancel(ctx), dagRun); rmErr != nil {
			logger.Error(ctx, "Failed to rollback queued DAG run",
				tag.DAG(req.DAG.Name),
				tag.RunID(req.DAGRunID),
				tag.Error(rmErr),
			)
		}
	}()

	status := queuedStatus(req, dagRun, attempt.ID(), logFile, artifactDir, now)
	writeResult, err := writeQueuedStatus(ctx, attempt, status, req.ProceedOnStatusCloseErr)
	if err != nil {
		return nil, err
	}

	if err := req.QueueStore.Enqueue(ctx, queueName, exec.QueuePriorityLow, dagRun); err != nil {
		return nil, joinCloseAndEnqueue(
			wrapCloseErr(writeResult.closeErr),
			fmt.Errorf("failed to enqueue DAG run: %w", err),
		)
	}
	committed = true

	return &QueuedRun{
		DAGRun:         dagRun,
		Attempt:        attempt,
		Status:         status,
		QueueName:      queueName,
		LogFile:        logFile,
		ArtifactDir:    artifactDir,
		StatusCloseErr: writeResult.closeErr,
	}, nil
}

func (r QueueRequest) validate() error {
	if r.DAGRunStore == nil {
		return fmt.Errorf("dag-run store is required")
	}
	if r.QueueStore == nil {
		return fmt.Errorf("queue store is required")
	}
	if r.DAG == nil {
		return fmt.Errorf("dag is required")
	}
	if r.DAGRunID == "" {
		return fmt.Errorf("dag-run ID is required")
	}
	return nil
}

func (r QueueRequest) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r QueueRequest) queueName() string {
	if r.QueueName != "" {
		return r.QueueName
	}
	return r.DAG.ProcGroup()
}

func artifactDir(ctx context.Context, req QueueRequest) (string, error) {
	if !req.DAG.ArtifactsEnabled() {
		return "", nil
	}

	dir, err := logpath.GenerateDir(ctx, req.ArtifactBaseDir, req.DAG.Artifacts.Dir, req.DAG.Name, req.DAGRunID)
	if err != nil {
		return "", fmt.Errorf("failed to generate artifact directory: %w", err)
	}
	return dir, nil
}

func queuedStatus(req QueueRequest, dagRun exec.DAGRunRef, attemptID, logFile, archiveDir string, now time.Time) exec.DAGRunStatus {
	root := req.Root
	if root.Zero() {
		root = dagRun
	}

	opts := []transform.StatusOption{
		transform.WithLogFilePath(logFile),
		transform.WithArchiveDir(archiveDir),
		transform.WithAttemptID(attemptID),
		transform.WithPreconditions(req.DAG.Preconditions),
		transform.WithQueuedAt(stringutil.FormatTime(now)),
		transform.WithHierarchyRefs(root, req.Parent),
		transform.WithTriggerType(req.TriggerType),
	}
	if req.ScheduleTime != "" {
		opts = append(opts, transform.WithScheduleTime(req.ScheduleTime))
	}

	return transform.NewStatusBuilder(req.DAG).Create(req.DAGRunID, core.Queued, 0, time.Time{}, opts...)
}

// queuedStatusWriteResult captures non-fatal status write side effects.
type queuedStatusWriteResult struct {
	// closeErr is set when closing the status attempt failed but the request
	// allowed queue publication to continue.
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
