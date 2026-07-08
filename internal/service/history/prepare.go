// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package history

import (
	"context"
	"errors"
	"fmt"

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
	logFile, err := logpath.Generate(ctx, s.cfg.LogBaseDir, cmd.DAG.LogDir, cmd.DAG.Name, cmd.DAGRunID)
	if err != nil {
		return nil, fmt.Errorf("failed to generate log file name: %w", err)
	}
	artifactDir, err := s.localArtifactDir(ctx, cmd.DAG, cmd.DAGRunID)
	if err != nil {
		return nil, err
	}

	if err := s.cfg.ProcStore.Lock(ctx, cmd.DAG.ProcGroup()); err != nil {
		return nil, fmt.Errorf("failed to lock process group: %w", err)
	}
	defer s.cfg.ProcStore.Unlock(ctx, cmd.DAG.ProcGroup())

	attempt, status, err := s.resolveLocalAttempt(ctx, cmd)
	if err != nil {
		if errors.Is(err, exec.ErrDAGRunAlreadyExists) {
			return nil, fmt.Errorf("%w: dag-run ID %s already exists for DAG %s", ErrLocalExecutionAlreadyExists, cmd.DAGRunID, cmd.DAG.Name)
		}
		return nil, fmt.Errorf("failed to prepare execution attempt: %w", err)
	}
	if attempt == nil {
		return nil, fmt.Errorf("attempt resolution returned nil attempt")
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
		Execution: newExecutionContext(
			exec.NewDAGRunRef(cmd.DAG.Name, cmd.DAGRunID),
			attempt,
			proc,
			preparedLogFile(logFile, status),
			preparedArtifactDir(artifactDir, status),
		),
		Status:        status,
		RollbackToken: preparedLocalAttemptRollbackToken(cmd),
	}, nil
}

func (s *Service) validatePrepareLocalAttempt(cmd PrepareLocalAttemptCommand) error {
	if s.cfg.DAGRunStore == nil {
		return fmt.Errorf("dag-run store is required")
	}
	if s.cfg.ProcStore == nil {
		return fmt.Errorf("proc store is required")
	}
	if cmd.DAG == nil {
		return fmt.Errorf("dag is required")
	}
	if cmd.DAGRunID == "" {
		return fmt.Errorf("dag-run ID is required")
	}
	if cmd.Mode == "" {
		return fmt.Errorf("attempt mode is required")
	}
	return nil
}

func (s *Service) resolveLocalAttempt(
	ctx context.Context,
	cmd PrepareLocalAttemptCommand,
) (exec.DAGRunAttempt, *exec.DAGRunStatus, error) {
	switch cmd.Mode {
	case PrepareAttemptCreate:
		attempt, err := s.cfg.DAGRunStore.CreateAttempt(ctx, cmd.DAG, s.now(), cmd.DAGRunID, cmd.AttemptOptions)
		return attempt, nil, err
	case PrepareAttemptOpenExisting:
		return s.openExistingRootAttempt(ctx, cmd)
	case PrepareAttemptOpenSub:
		return s.openExistingSubAttempt(ctx, cmd)
	default:
		return nil, nil, fmt.Errorf("unknown attempt mode %q", cmd.Mode)
	}
}

func (s *Service) openExistingRootAttempt(
	ctx context.Context,
	cmd PrepareLocalAttemptCommand,
) (exec.DAGRunAttempt, *exec.DAGRunStatus, error) {
	attempt, err := s.cfg.DAGRunStore.FindAttempt(ctx, exec.NewDAGRunRef(cmd.DAG.Name, cmd.DAGRunID))
	if err != nil {
		return nil, nil, err
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, nil, err
	}
	if err := validatePreparedAttemptBinding(cmd.DAGRunID, cmd.AttemptID, attempt, status); err != nil {
		return nil, nil, err
	}
	return attempt, status, nil
}

func (s *Service) openExistingSubAttempt(
	ctx context.Context,
	cmd PrepareLocalAttemptCommand,
) (exec.DAGRunAttempt, *exec.DAGRunStatus, error) {
	if cmd.Root.Zero() || cmd.Root.ID == cmd.DAGRunID {
		return nil, nil, fmt.Errorf("root dag-run is required for sub-attempt")
	}
	attempt, err := s.cfg.DAGRunStore.FindSubAttempt(ctx, cmd.Root, cmd.DAGRunID)
	if err != nil {
		return nil, nil, err
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, nil, err
	}
	if err := validatePreparedAttemptBinding(cmd.DAGRunID, cmd.AttemptID, attempt, status); err != nil {
		return nil, nil, err
	}
	return attempt, status, nil
}

func validatePreparedAttemptBinding(
	dagRunID, requestedAttemptID string,
	attempt exec.DAGRunAttempt,
	status *exec.DAGRunStatus,
) error {
	if requestedAttemptID == "" {
		return nil
	}
	currentAttemptID := requestedAttemptID
	if status != nil && status.AttemptID != "" {
		currentAttemptID = status.AttemptID
	} else if attempt != nil && attempt.ID() != "" {
		currentAttemptID = attempt.ID()
	}
	if currentAttemptID != requestedAttemptID {
		return fmt.Errorf(
			"distributed worker attempt %q is stale for dag-run %s; latest attempt is %q",
			requestedAttemptID,
			dagRunID,
			currentAttemptID,
		)
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
	logFile, archiveDir := s.failurePaths(
		ctx,
		cmd.DAG,
		cmd.DAGRunID,
		"Failed to generate log file path for prepared local execution failure",
		"Failed to generate artifact directory for prepared local execution failure",
	)

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

	return writeFailedStatus(ctx, attempt, status, "failed to open attempt for failure recording")
}

func preparedLogFile(logFile string, status *exec.DAGRunStatus) string {
	if status != nil && status.Log != "" {
		return status.Log
	}
	return logFile
}

func preparedArtifactDir(artifactDir string, status *exec.DAGRunStatus) string {
	if status != nil && status.ArchiveDir != "" {
		return status.ArchiveDir
	}
	return artifactDir
}

func preparedLocalAttemptRollbackToken(cmd PrepareLocalAttemptCommand) PreparedLocalAttemptRollbackToken {
	if cmd.Mode != PrepareAttemptCreate || cmd.AttemptOptions.RootDAGRun != nil {
		return PreparedLocalAttemptRollbackToken{}
	}
	return PreparedLocalAttemptRollbackToken{
		dagRun: exec.NewDAGRunRef(cmd.DAG.Name, cmd.DAGRunID),
	}
}
