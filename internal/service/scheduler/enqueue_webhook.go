// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/dagrun/intake"
)

// EnqueueWebhookRun enqueues a webhook-triggered run while preserving the same
// runtime-param semantics as direct webhook execution.
func EnqueueWebhookRun(
	ctx context.Context,
	dagRunStore exec.DAGRunStore,
	queueStore exec.QueueStore,
	baseLogDir string,
	baseArtifactDir string,
	baseConfig string,
	dag *core.DAG,
	runID string,
	params string,
	now time.Time,
) error {
	dagRun := exec.NewDAGRunRef(dag.Name, runID)

	if _, err := dagRunStore.FindAttempt(ctx, dagRun); err == nil {
		logger.Info(ctx, "Webhook run already exists; skipping",
			tag.DAG(dag.Name),
			tag.RunID(runID),
		)
		return nil
	} else if !errors.Is(err, exec.ErrDAGRunIDNotFound) {
		return fmt.Errorf("failed to check existing webhook run: %w", err)
	}

	fullDAG, err := rehydrateExecutionDAG(ctx, dag, params, baseConfig)
	if err != nil {
		return fmt.Errorf("failed to load full DAG for webhook enqueue: %w", err)
	}
	if fullDAG == nil {
		return fmt.Errorf("failed to load full DAG for webhook enqueue: DAG is nil")
	}

	dagCopy := fullDAG.Clone()
	dagCopy.Location = ""

	_, err = intake.EnqueueRun(ctx, intake.QueueRequest{
		DAGRunStore:     dagRunStore,
		QueueStore:      queueStore,
		DAG:             dagCopy,
		DAGRunID:        runID,
		LogBaseDir:      baseLogDir,
		ArtifactBaseDir: baseArtifactDir,
		TriggerType:     core.TriggerTypeWebhook,
		Now:             func() time.Time { return now },
	})
	if err != nil {
		return fmt.Errorf("failed to enqueue webhook run: %w", err)
	}

	logger.Info(ctx, "Webhook run enqueued",
		tag.DAG(dag.Name),
		tag.RunID(runID),
	)

	return nil
}
