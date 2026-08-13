// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/logpath"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/proc"
)

var (
	ErrLocalExecutionAlreadyExists = errors.New("local execution already exists")
	ErrProcAcquisitionFailed       = errors.New("failed to acquire process handle")
)

// LocalAttemptBuilder creates or resolves the attempt that a local execution
// will own.
type LocalAttemptBuilder func(context.Context) (dagrun.Attempt, error)

// LocalProcRepository is the process-repository surface needed to claim local
// execution ownership.
type LocalProcRepository interface {
	WithLock(ctx context.Context, groupName string, fn func() error) error
	Acquire(ctx context.Context, groupName string, meta proc.ProcMeta) (proc.ProcHandle, error)
}

// LocalRequest describes local DAG-run intake before execution starts.
type LocalRequest struct {
	ProcRepository LocalProcRepository
	DAG            *ir.DAG
	DAGRunID       string
	DefinitionID   string

	Root         ir.DAGRunRef
	Parent       ir.DAGRunRef
	TriggerType  ir.TriggerType
	TriggerActor string

	ScheduleTime string
	ProfileName  string

	LogBaseDir      string
	ArtifactBaseDir string

	BuildAttempt LocalAttemptBuilder
}

// LocalPreparation is the successfully prepared local execution ownership.
type LocalPreparation struct {
	Attempt dagrun.Attempt
	Proc    proc.ProcHandle
}

// PrepareLocalExecution creates or resolves the execution attempt, acquires the
// local process heartbeat, and records a failed status if heartbeat acquisition
// fails after an attempt was prepared.
func PrepareLocalExecution(ctx context.Context, req LocalRequest) (*LocalPreparation, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	if req.Root.Zero() {
		req.Root = ir.NewDAGRunRef(req.DAG.Name, req.DAGRunID)
	}

	var preparation *LocalPreparation
	err := req.ProcRepository.WithLock(ctx, req.DAG.ProcGroup(), func() error {
		attempt, err := req.BuildAttempt(ctx)
		if err != nil {
			if errors.Is(err, dagrun.ErrDAGRunAlreadyExists) {
				return fmt.Errorf("%w: dag-run ID %s already exists for DAG %s", ErrLocalExecutionAlreadyExists, req.DAGRunID, req.DAG.Name)
			}
			return fmt.Errorf("failed to prepare execution attempt: %w", err)
		}
		if attempt == nil {
			return fmt.Errorf("attempt builder returned nil attempt")
		}
		attempt.SetDAG(req.DAG)

		handle, err := req.ProcRepository.Acquire(ctx, req.DAG.ProcGroup(), proc.ProcMeta{
			StartedAt:    time.Now().Unix(),
			Name:         req.DAG.Name,
			DAGRunID:     req.DAGRunID,
			AttemptID:    attempt.ID(),
			RootName:     req.Root.Name,
			RootDAGRunID: req.Root.ID,
		})
		if err != nil {
			if recErr := recordPreparedAttemptFailure(ctx, req, attempt, err); recErr != nil {
				return errors.Join(
					fmt.Errorf("%w: %w", ErrProcAcquisitionFailed, err),
					fmt.Errorf("failed to record prepared local execution failure: %w", recErr),
				)
			}
			return fmt.Errorf("%w: %w", ErrProcAcquisitionFailed, err)
		}
		preparation = &LocalPreparation{Attempt: attempt, Proc: handle}
		return nil
	})
	if err != nil {
		if persis.IsProcLockError(err) {
			return nil, fmt.Errorf("failed to lock process group: %w", err)
		}
		return nil, err
	}
	return preparation, nil
}

func (r LocalRequest) validate() error {
	if r.ProcRepository == nil {
		return fmt.Errorf("proc store is required")
	}
	if r.DAG == nil {
		return fmt.Errorf("dag is required")
	}
	if r.DAGRunID == "" {
		return fmt.Errorf("dag-run ID is required")
	}
	if r.BuildAttempt == nil {
		return fmt.Errorf("attempt builder is required")
	}
	return nil
}

func recordPreparedAttemptFailure(
	ctx context.Context,
	req LocalRequest,
	attempt dagrun.Attempt,
	runErr error,
) error {
	logFile, logErr := logpath.Generate(ctx, req.LogBaseDir, req.DAG.LogDir, req.DAG.Name, req.DAGRunID)
	if logErr != nil {
		logger.Warn(ctx, "Failed to generate log file path for prepared local execution failure",
			tag.Error(logErr),
			tag.DAG(req.DAG.Name),
			tag.RunID(req.DAGRunID),
		)
	}

	archiveDir, archiveErr := localArtifactDir(ctx, req)
	if archiveErr != nil {
		logger.Warn(ctx, "Failed to generate artifact directory for prepared local execution failure",
			tag.Error(archiveErr),
			tag.DAG(req.DAG.Name),
			tag.RunID(req.DAGRunID),
		)
	}

	opts := []ir.StatusOption{
		ir.WithAttemptID(attempt.ID()),
		ir.WithDAGDefinitionID(req.DefinitionID),
		ir.WithHierarchyRefs(req.Root, req.Parent),
		ir.WithLogFilePath(logFile),
		ir.WithArchiveDir(archiveDir),
		ir.WithFinishedAt(time.Now()),
		ir.WithError(runErr.Error()),
		ir.WithWorkerID("local"),
		ir.WithTriggerType(req.TriggerType),
		ir.WithTriggerActor(req.TriggerActor),
		ir.WithRuntimeProfile(req.ProfileName, "", nil),
	}
	if req.ScheduleTime != "" {
		opts = append(opts, ir.WithScheduleTime(req.ScheduleTime))
	}
	status := ir.NewStatusBuilder(req.DAG).Create(req.DAGRunID, ir.Failed, 0, time.Now(), opts...)

	if err := attempt.Open(ctx); err != nil {
		return fmt.Errorf("failed to open attempt for failure recording: %w", err)
	}
	defer func() {
		_ = attempt.Close(ctx)
	}()

	if err := attempt.Write(ctx, status); err != nil {
		return fmt.Errorf("failed to write failed status: %w", err)
	}
	return nil
}

func localArtifactDir(ctx context.Context, req LocalRequest) (string, error) {
	if !req.DAG.ArtifactsEnabled() {
		return "", nil
	}
	return logpath.GenerateDir(ctx, req.ArtifactBaseDir, req.DAG.Artifacts.Dir, req.DAG.Name, req.DAGRunID)
}
