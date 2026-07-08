// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package history

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime/transform"
)

func (s *Service) recordEarlyFailure(ctx context.Context, cmd RecordEarlyFailureRequest) error {
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

	logPath, artifactDir := s.failurePaths(
		ctx,
		cmd.DAG,
		cmd.DAGRunID,
		"Failed to generate log file path for early failure status",
		"Failed to generate artifact directory for early failure status",
	)

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

	return writeFailedStatus(ctx, attempt, status, "failed to open attempt for recording failure")
}

func (s *Service) validateRecordEarlyFailure(cmd RecordEarlyFailureRequest) error {
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
