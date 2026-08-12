// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/intake"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	queuedomain "github.com/dagucloud/dagu/v2/internal/queue"
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
	dagRunRepository *persis.DAGRunRepository,
	queueStore queuedomain.QueueStore,
	baseLogDir string,
	baseArtifactDir string,
	baseConfig string,
	workspaceBaseConfigDir string,
	definitionID string,
	dag *ir.DAG,
	runID string,
	triggerType ir.TriggerType,
	scheduleTime time.Time,
	profileName string,
) error {
	dagRun := ir.NewDAGRunRef(dag.Name, runID)

	// Idempotency: skip if a run with this ID already exists.
	if _, err := dagRunRepository.FindAttempt(ctx, dagRun); err == nil {
		logger.Info(ctx, "Catchup run already exists; skipping",
			tag.DAG(dag.Name),
			tag.RunID(runID),
		)
		return nil
	} else if !errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
		return fmt.Errorf("failed to check existing catchup run: %w", err)
	}

	fullDAG, err := rehydrateExecutionDAG(ctx, dag, nil, baseConfig, workspaceBaseConfigDir)
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
		DAGRunRepository: dagRunRepository,
		QueueStore:       queueStore,
		DAG:              dagCopy,
		DAGRunID:         runID,
		LogBaseDir:       baseLogDir,
		ArtifactBaseDir:  baseArtifactDir,
		TriggerType:      triggerType,
		ScheduleTime:     stringutil.FormatTime(scheduleTime),
		ProfileName:      profileName,
		DefinitionID:     definitionID,
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
