// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/dagrun/intake"
)

// EnqueueCatchupRun enqueues a catchup run for a DAG.
//
// The function is idempotent: if a run with the same ID already exists
// (checked via FindAttempt), it returns nil without creating a duplicate.
//
// On failure after CreateAttempt but before Enqueue, the orphaned attempt
// record is cleaned up via RemoveDAGRun.
//
// The DAG is reloaded from source before persistence so queued catchup retries
// inherit a complete execution snapshot. The reloaded DAG is then shallow-copied
// to avoid mutating the shared planner entry (Location is cleared to prevent
// unix pipe conflicts for concurrent runs).
func EnqueueCatchupRun(
	ctx context.Context,
	dagRunStore exec.DAGRunStore,
	queueStore exec.QueueStore,
	baseLogDir string,
	baseArtifactDir string,
	baseConfig string,
	dag *core.DAG,
	runID string,
	triggerType core.TriggerType,
	scheduleTime time.Time,
	profileName string,
) error {
	dagRun := exec.NewDAGRunRef(dag.Name, runID)

	// Idempotency: skip if a run with this ID already exists.
	if _, err := dagRunStore.FindAttempt(ctx, dagRun); err == nil {
		logger.Info(ctx, "Catchup run already exists; skipping",
			tag.DAG(dag.Name),
			tag.RunID(runID),
		)
		return nil
	}

	fullDAG, err := rehydrateExecutionDAG(ctx, dag, nil, baseConfig)
	if err != nil {
		return fmt.Errorf("failed to load full DAG for catchup enqueue: %w", err)
	}
	if fullDAG == nil {
		return fmt.Errorf("failed to load full DAG for catchup enqueue: DAG is nil")
	}
	// Clone to avoid mutating the shared planner entry.
	// Location is cleared to prevent unix pipe conflicts for concurrent runs
	// (same as cmd/enqueue.go:87).
	dagCopy := fullDAG.Clone()
	dagCopy.Location = ""

	_, err = intake.EnqueueRun(ctx, intake.QueueRequest{
		DAGRunStore:     dagRunStore,
		QueueStore:      queueStore,
		DAG:             dagCopy,
		DAGRunID:        runID,
		LogBaseDir:      baseLogDir,
		ArtifactBaseDir: baseArtifactDir,
		TriggerType:     triggerType,
		ScheduleTime:    stringutil.FormatTime(scheduleTime),
		ProfileName:     profileName,
	})
	if err != nil {
		return fmt.Errorf("failed to enqueue catchup run: %w", err)
	}

	logger.Info(ctx, "Catchup run enqueued",
		tag.DAG(dag.Name),
		tag.RunID(runID),
	)

	return nil
}
