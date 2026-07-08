// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package history

import (
	"context"
	"errors"
	"fmt"

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

// LocalProcStore is the proc-store surface needed to claim local execution ownership.
type LocalProcStore interface {
	Lock(ctx context.Context, groupName string) error
	Unlock(ctx context.Context, groupName string)
	Acquire(ctx context.Context, groupName string, meta exec.ProcMeta) (exec.ProcHandle, error)
}

func (s *Service) prepareLocalAttempt(ctx context.Context, cmd PrepareLocalAttemptCommand) (*PreparedLocalAttempt, error) {
	if err := s.validatePrepareLocalAttempt(cmd); err != nil {
		return nil, err
	}
	if cmd.Root.Zero() {
		cmd.Root = exec.NewDAGRunRef(cmd.DAG.Name, cmd.DAGRunID)
	}

	if err := s.cfg.ProcStore.Lock(ctx, cmd.DAG.ProcGroup()); err != nil {
		return nil, fmt.Errorf("failed to lock process group: %w", err)
	}
	defer s.cfg.ProcStore.Unlock(ctx, cmd.DAG.ProcGroup())

	attempt, err := cmd.BuildAttempt(ctx)
	if err != nil {
		if errors.Is(err, exec.ErrDAGRunAlreadyExists) {
			return nil, fmt.Errorf("%w: dag-run ID %s already exists for DAG %s", ErrLocalExecutionAlreadyExists, cmd.DAGRunID, cmd.DAG.Name)
		}
		return nil, fmt.Errorf("failed to prepare execution attempt: %w", err)
	}
	if attempt == nil {
		return nil, fmt.Errorf("attempt builder returned nil attempt")
	}
	attempt.SetDAG(cmd.DAG)

	now := s.now()
	proc, err := s.cfg.ProcStore.Acquire(ctx, cmd.DAG.ProcGroup(), exec.ProcMeta{
		StartedAt:    now.Unix(),
		Name:         cmd.DAG.Name,
		DAGRunID:     cmd.DAGRunID,
		AttemptID:    attempt.ID(),
		RootName:     cmd.Root.Name,
		RootDAGRunID: cmd.Root.ID,
	})
	if err != nil {
		if recErr := s.recordPreparedAttemptFailure(ctx, cmd, attempt, err); recErr != nil {
			return nil, errors.Join(
				fmt.Errorf("%w: %w", ErrProcAcquisitionFailed, err),
				fmt.Errorf("failed to record prepared local execution failure: %w", recErr),
			)
		}
		return nil, fmt.Errorf("%w: %w", ErrProcAcquisitionFailed, err)
	}

	return &PreparedLocalAttempt{
		Attempt: attempt,
		Proc:    proc,
	}, nil
}

func (s *Service) validatePrepareLocalAttempt(cmd PrepareLocalAttemptCommand) error {
	if s.cfg.ProcStore == nil {
		return fmt.Errorf("proc store is required")
	}
	if cmd.DAG == nil {
		return fmt.Errorf("dag is required")
	}
	if cmd.DAGRunID == "" {
		return fmt.Errorf("dag-run ID is required")
	}
	if cmd.BuildAttempt == nil {
		return fmt.Errorf("attempt builder is required")
	}
	return nil
}

func (s *Service) recordPreparedAttemptFailure(
	ctx context.Context,
	cmd PrepareLocalAttemptCommand,
	attempt exec.DAGRunAttempt,
	runErr error,
) error {
	now := s.now()
	logFile, logErr := logpath.Generate(ctx, s.cfg.LogBaseDir, cmd.DAG.LogDir, cmd.DAG.Name, cmd.DAGRunID)
	if logErr != nil {
		logger.Warn(ctx, "Failed to generate log file path for prepared local execution failure",
			tag.Error(logErr),
			tag.DAG(cmd.DAG.Name),
			tag.RunID(cmd.DAGRunID),
		)
	}

	archiveDir, archiveErr := s.localArtifactDir(ctx, cmd)
	if archiveErr != nil {
		logger.Warn(ctx, "Failed to generate artifact directory for prepared local execution failure",
			tag.Error(archiveErr),
			tag.DAG(cmd.DAG.Name),
			tag.RunID(cmd.DAGRunID),
		)
	}

	opts := []transform.StatusOption{
		transform.WithAttemptID(attempt.ID()),
		transform.WithHierarchyRefs(cmd.Root, cmd.Parent),
		transform.WithLogFilePath(logFile),
		transform.WithArchiveDir(archiveDir),
		transform.WithFinishedAt(now),
		transform.WithError(runErr.Error()),
		transform.WithWorkerID("local"),
		transform.WithTriggerType(cmd.TriggerType),
		transform.WithRuntimeProfile(cmd.ProfileName, "", nil),
	}
	if cmd.ScheduleTime != "" {
		opts = append(opts, transform.WithScheduleTime(cmd.ScheduleTime))
	}
	status := transform.NewStatusBuilder(cmd.DAG).Create(cmd.DAGRunID, core.Failed, 0, now, opts...)

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

func (s *Service) localArtifactDir(ctx context.Context, cmd PrepareLocalAttemptCommand) (string, error) {
	if !cmd.DAG.ArtifactsEnabled() {
		return "", nil
	}
	return logpath.GenerateDir(ctx, s.cfg.ArtifactBaseDir, cmd.DAG.Artifacts.Dir, cmd.DAG.Name, cmd.DAGRunID)
}
