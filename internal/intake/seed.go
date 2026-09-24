// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"context"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/logpath"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/transform"
)

// SeedRequest describes a new DAG-run whose first attempt is persisted as
// queued with fixed node states, to be executed through the retry path.
type SeedRequest struct {
	DAGRunRepository *persis.DAGRunRepository
	DAG              *ir.DAG
	DAGRunID         string
	Nodes            []runtime.NodeData

	// Source is the run whose recorded outputs the skipped nodes carry. When
	// set, its work directory is copied into the new run and the carried
	// output variables are rewritten to point at the copy.
	Source *ir.DAGRunStatus

	Params       string
	TriggerType  ir.TriggerType
	TriggerActor string
	ProfileName  string
	DefinitionID string
	NoReuse      bool

	LogBaseDir      string
	ArtifactBaseDir string
}

// SeedRun creates the queued attempt described by req and returns it with the
// status it recorded. The attempt is removed again if any later step fails.
func SeedRun(ctx context.Context, req SeedRequest) (dagrun.Attempt, *ir.DAGRunStatus, error) {
	if err := req.validate(); err != nil {
		return nil, nil, err
	}

	repo := req.DAGRunRepository
	dag := req.DAG
	now := time.Now()
	attempt, err := repo.CreateAttempt(ctx, dag, now, req.DAGRunID, persis.DAGRunCreateAttemptOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create seeded attempt: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if rmErr := repo.RemoveDAGRun(context.WithoutCancel(ctx), ir.NewDAGRunRef(dag.Name, req.DAGRunID), persis.DAGRunRemoveOptions{}); rmErr != nil {
			logger.Error(ctx, "Failed to rollback seeded attempt",
				tag.DAG(dag.Name),
				tag.RunID(req.DAGRunID),
				tag.Error(rmErr),
			)
		}
	}()

	logFile, err := logpath.Generate(ctx, req.LogBaseDir, dag.LogDir, dag.Name, req.DAGRunID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate seeded log file: %w", err)
	}
	artifactDir, err := runArtifactDir(ctx, req.ArtifactBaseDir, dag, req.DAGRunID)
	if err != nil {
		return nil, nil, err
	}

	status := ir.NewStatusBuilder(dag).Create(req.DAGRunID, ir.Queued, 0, time.Time{},
		transform.WithNodes(req.Nodes),
		ir.WithLogFilePath(logFile),
		ir.WithArchiveDir(artifactDir),
		ir.WithAttemptID(attempt.ID()),
		ir.WithQueuedAt(stringutil.FormatTime(now)),
		ir.WithPreconditions(dag.Preconditions),
		ir.WithHierarchyRefs(ir.NewDAGRunRef(dag.Name, req.DAGRunID), ir.DAGRunRef{}),
		ir.WithTriggerType(req.TriggerType),
		ir.WithTriggerActor(req.TriggerActor),
		ir.WithRuntimeProfile(req.ProfileName, "", nil),
		ir.WithDAGDefinitionID(req.DefinitionID),
		ir.WithNoReuse(req.NoReuse),
	)
	status.Params = req.Params
	status.ParamsList = dag.Params
	targetWorkDirRef := dagrun.WorkDirRef{DAGRun: ir.NewDAGRunRef(dag.Name, req.DAGRunID)}
	targetWorkDir, err := repo.MaterializeWorkDir(ctx, targetWorkDirRef)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to materialize seeded work directory: %w", err)
	}

	if err := attempt.Open(ctx); err != nil {
		return nil, nil, fmt.Errorf("failed to open seeded attempt: %w", err)
	}
	if req.Source != nil && hasSkippedByRetryNode(req.Nodes) {
		sourceWorkDir, err := repo.MaterializeWorkDir(ctx, dagrun.WorkDirRef{RootDAGRun: req.Source.Root, DAGRun: req.Source.DAGRun()})
		if err != nil {
			_ = attempt.Close(ctx)
			return nil, nil, fmt.Errorf("failed to materialize source work directory: %w", err)
		}
		if err := copyWorkDir(sourceWorkDir, targetWorkDir); err != nil {
			_ = attempt.Close(ctx)
			return nil, nil, fmt.Errorf("failed to copy source work directory: %w", err)
		}
		remapWorkDirOutputs(status.Nodes, sourceWorkDir, targetWorkDir)
	}

	if err := attempt.Write(ctx, status); err != nil {
		_ = attempt.Close(ctx)
		return nil, nil, fmt.Errorf("failed to save seeded status: %w", err)
	}
	if err := attempt.Close(ctx); err != nil {
		return nil, nil, fmt.Errorf("failed to close seeded attempt: %w", err)
	}
	if err := repo.SnapshotWorkDir(ctx, targetWorkDirRef, targetWorkDir); err != nil {
		return nil, nil, fmt.Errorf("failed to snapshot seeded work directory: %w", err)
	}
	committed = true

	return attempt, &status, nil
}

// MarkSeedFailed records cause on a seeded run that never left the queued
// state, so a launch failure does not leave the run queued forever.
func MarkSeedFailed(ctx context.Context, repo *persis.DAGRunRepository, status *ir.DAGRunStatus, cause error) {
	if repo == nil || status == nil || cause == nil {
		return
	}
	_, _, err := repo.CompareAndSwapLatestAttemptStatus(
		ctx,
		status.DAGRun(),
		status.AttemptID,
		ir.Queued,
		func(latest *ir.DAGRunStatus) error {
			latest.Status = ir.Failed
			latest.FinishedAt = stringutil.FormatTime(time.Now())
			latest.Error = cause.Error()
			return nil
		}, persis.DAGRunCompareAndSwapOptions{},
	)
	if err != nil {
		logger.Warn(ctx, "Failed to mark seeded run as failed",
			tag.DAG(status.Name),
			tag.RunID(status.DAGRunID),
			tag.Error(err),
		)
	}
}

func (r SeedRequest) validate() error {
	if r.DAGRunRepository == nil {
		return fmt.Errorf("DAG-run repository is required")
	}
	if r.DAG == nil {
		return fmt.Errorf("dag is required")
	}
	if r.DAGRunID == "" {
		return fmt.Errorf("dag-run ID is required")
	}
	return nil
}

func hasSkippedByRetryNode(nodes []runtime.NodeData) bool {
	for _, node := range nodes {
		if node.State.SkippedByRetry {
			return true
		}
	}
	return false
}
