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
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime/transform"
)

var (
	ErrLocalExecutionAlreadyExists = errors.New("local execution already exists")
	ErrProcAcquisitionFailed       = errors.New("failed to acquire process handle")
)

// LocalAttemptBuilder creates or resolves the attempt that a local execution
// will own.
type LocalAttemptBuilder func(context.Context) (exec.DAGRunAttempt, error)

// LocalProcStore is the proc-store surface needed to claim local execution
// ownership.
type LocalProcStore interface {
	Lock(ctx context.Context, groupName string) error
	Unlock(ctx context.Context, groupName string)
	Acquire(ctx context.Context, groupName string, meta exec.ProcMeta) (exec.ProcHandle, error)
}

// LocalRequest describes local DAG-run intake before execution starts.
type LocalRequest struct {
	ProcStore LocalProcStore
	DAG       *core.DAG
	DAGRunID  string

	Root        exec.DAGRunRef
	Parent      exec.DAGRunRef
	TriggerType core.TriggerType

	ScheduleTime string
	ProfileName  string

	LogBaseDir      string
	ArtifactBaseDir string

	BuildAttempt LocalAttemptBuilder
}

// LocalPreparation is the successfully prepared local execution ownership.
type LocalPreparation struct {
	Attempt exec.DAGRunAttempt
	Proc    exec.ProcHandle
}

// PrepareLocalExecution creates or resolves the execution attempt, acquires the
// local process heartbeat, and records a failed status if heartbeat acquisition
// fails after an attempt was prepared.
func PrepareLocalExecution(ctx context.Context, req LocalRequest) (*LocalPreparation, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	if req.Root.Zero() {
		req.Root = exec.NewDAGRunRef(req.DAG.Name, req.DAGRunID)
	}

	if err := req.ProcStore.Lock(ctx, req.DAG.ProcGroup()); err != nil {
		return nil, fmt.Errorf("failed to lock process group: %w", err)
	}
	defer req.ProcStore.Unlock(ctx, req.DAG.ProcGroup())

	attempt, err := req.BuildAttempt(ctx)
	if err != nil {
		if errors.Is(err, exec.ErrDAGRunAlreadyExists) {
			return nil, fmt.Errorf("%w: dag-run ID %s already exists for DAG %s", ErrLocalExecutionAlreadyExists, req.DAGRunID, req.DAG.Name)
		}
		return nil, fmt.Errorf("failed to prepare execution attempt: %w", err)
	}
	if attempt == nil {
		return nil, fmt.Errorf("attempt builder returned nil attempt")
	}
	attempt.SetDAG(req.DAG)

	proc, err := req.ProcStore.Acquire(ctx, req.DAG.ProcGroup(), exec.ProcMeta{
		StartedAt:    time.Now().Unix(),
		Name:         req.DAG.Name,
		DAGRunID:     req.DAGRunID,
		AttemptID:    attempt.ID(),
		RootName:     req.Root.Name,
		RootDAGRunID: req.Root.ID,
	})
	if err != nil {
		if recErr := recordPreparedAttemptFailure(ctx, req, attempt, err); recErr != nil {
			return nil, errors.Join(
				fmt.Errorf("%w: %w", ErrProcAcquisitionFailed, err),
				fmt.Errorf("failed to record prepared local execution failure: %w", recErr),
			)
		}
		return nil, fmt.Errorf("%w: %w", ErrProcAcquisitionFailed, err)
	}

	return &LocalPreparation{
		Attempt: attempt,
		Proc:    proc,
	}, nil
}

func (r LocalRequest) validate() error {
	if r.ProcStore == nil {
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
	attempt exec.DAGRunAttempt,
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

	opts := []transform.StatusOption{
		transform.WithAttemptID(attempt.ID()),
		transform.WithHierarchyRefs(req.Root, req.Parent),
		transform.WithLogFilePath(logFile),
		transform.WithArchiveDir(archiveDir),
		transform.WithFinishedAt(time.Now()),
		transform.WithError(runErr.Error()),
		transform.WithWorkerID("local"),
		transform.WithTriggerType(req.TriggerType),
		transform.WithRuntimeProfile(req.ProfileName, "", nil),
	}
	if req.ScheduleTime != "" {
		opts = append(opts, transform.WithScheduleTime(req.ScheduleTime))
	}
	status := transform.NewStatusBuilder(req.DAG).Create(req.DAGRunID, core.Failed, 0, time.Now(), opts...)

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
