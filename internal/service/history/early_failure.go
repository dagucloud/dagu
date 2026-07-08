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

func (s *Service) recordEarlyFailure(ctx context.Context, cmd RecordEarlyFailureCommand) error {
	if err := s.validateRecordEarlyFailure(cmd); err != nil {
		return err
	}

	ref := exec.NewDAGRunRef(cmd.DAG.Name, cmd.DAGRunID)
	attempt, findErr := s.cfg.DAGRunStore.FindAttempt(ctx, ref)
	if findErr != nil && !errors.Is(findErr, exec.ErrDAGRunIDNotFound) {
		return fmt.Errorf("failed to check for existing attempt: %w", findErr)
	}
	if attempt == nil {
		created, createErr := s.cfg.DAGRunStore.CreateAttempt(ctx, cmd.DAG, s.now(), cmd.DAGRunID, exec.NewDAGRunAttemptOptions{})
		if createErr != nil {
			return fmt.Errorf("failed to create run to record failure: %w", createErr)
		}
		attempt = created
	}

	logPath, logErr := logpath.Generate(ctx, s.cfg.LogBaseDir, cmd.DAG.LogDir, cmd.DAG.Name, cmd.DAGRunID)
	if logErr != nil {
		logger.Warn(ctx, "Failed to generate log file path for early failure status",
			tag.Error(logErr),
			tag.DAG(cmd.DAG.Name),
			tag.RunID(cmd.DAGRunID),
		)
	}
	artifactDir, artifactErr := s.localArtifactDir(ctx, PrepareLocalAttemptCommand{
		DAG:      cmd.DAG,
		DAGRunID: cmd.DAGRunID,
	})
	if artifactErr != nil {
		logger.Warn(ctx, "Failed to generate artifact directory for early failure status",
			tag.Error(artifactErr),
			tag.DAG(cmd.DAG.Name),
			tag.RunID(cmd.DAGRunID),
		)
	}

	now := s.now()
	status := transform.NewStatusBuilder(cmd.DAG).Create(
		cmd.DAGRunID,
		core.Failed,
		0,
		now,
		transform.WithLogFilePath(logPath),
		transform.WithArchiveDir(artifactDir),
		transform.WithFinishedAt(now),
		transform.WithError(cmd.Err.Error()),
	)

	if err := attempt.Open(ctx); err != nil {
		return fmt.Errorf("failed to open attempt for recording failure: %w", err)
	}
	defer func() {
		_ = attempt.Close(ctx)
	}()

	if err := attempt.Write(ctx, status); err != nil {
		return fmt.Errorf("failed to write failed status: %w", err)
	}
	return nil
}

func (s *Service) validateRecordEarlyFailure(cmd RecordEarlyFailureCommand) error {
	if s.cfg.DAGRunStore == nil {
		return fmt.Errorf("dag-run store is required")
	}
	if cmd.DAG == nil || cmd.DAGRunID == "" {
		return fmt.Errorf("DAG and dag-run ID are required to record failure")
	}
	if cmd.Err == nil {
		return fmt.Errorf("error is required to record failure")
	}
	return nil
}
