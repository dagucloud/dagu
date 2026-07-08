// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"context"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/history"
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
	ProfileName  string

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

// EnqueueRun preserves the legacy queue-backed DAG-run submission API.
func EnqueueRun(ctx context.Context, req QueueRequest) (*QueuedRun, error) {
	if req.QueueStore == nil {
		return nil, fmt.Errorf("queue store is required")
	}

	historySvc := history.New(history.Config{
		DAGRunStore:     req.DAGRunStore,
		LogBaseDir:      req.LogBaseDir,
		ArtifactBaseDir: req.ArtifactBaseDir,
		Now:             req.Now,
		Scheduler: history.ScheduleFunc(func(callCtx context.Context, schedule history.ScheduleRequest) error {
			return req.QueueStore.Enqueue(callCtx, schedule.QueueName, schedule.Priority, schedule.DAGRun)
		}),
	})
	submitted, err := historySvc.SubmitRun(ctx, history.SubmitRunCommand{
		DAG:                     req.DAG,
		DAGRunID:                req.DAGRunID,
		QueueName:               req.QueueName,
		Root:                    req.Root,
		Parent:                  req.Parent,
		TriggerType:             req.TriggerType,
		ScheduleTime:            req.ScheduleTime,
		ProfileName:             req.ProfileName,
		AttemptOptions:          req.AttemptOptions,
		ProceedOnStatusCloseErr: req.ProceedOnStatusCloseErr,
	})
	if err != nil {
		return nil, err
	}

	return &QueuedRun{
		DAGRun:         submitted.DAGRun,
		Attempt:        submitted.Attempt,
		Status:         submitted.Status,
		QueueName:      submitted.QueueName,
		LogFile:        submitted.LogFile,
		ArtifactDir:    submitted.ArtifactDir,
		StatusCloseErr: submitted.StatusCloseErr,
	}, nil
}
