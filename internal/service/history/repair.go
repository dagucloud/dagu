// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package history

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/internal/cmn/logpath"
	"github.com/dagucloud/dagu/internal/core/exec"
)

func (s *Service) repairQueuedCatchupRun(ctx context.Context, cmd RepairQueuedCatchupRunRequest) error {
	if !exec.IsQueuedCatchup(cmd.Status) {
		return nil
	}
	if cmd.DAG == nil {
		return fmt.Errorf("dag is required")
	}
	if cmd.Status.Log != "" && (!cmd.DAG.ArtifactsEnabled() || cmd.Status.ArchiveDir != "") {
		return nil
	}
	if s.cfg.DAGRunStore == nil {
		return fmt.Errorf("dag-run store is required")
	}
	if err := validateDAGRunRef(cmd.Status.DAGRun()); err != nil {
		return err
	}

	if cmd.Status.Log == "" {
		logPath, err := logpath.Generate(ctx, s.cfg.LogBaseDir, cmd.DAG.LogDir, cmd.DAG.Name, cmd.Status.DAGRunID)
		if err != nil {
			return fmt.Errorf("failed to generate queued catchup log file: %w", err)
		}
		cmd.Status.Log = logPath
	}
	if cmd.DAG.ArtifactsEnabled() && cmd.Status.ArchiveDir == "" {
		artifactDir, err := s.localArtifactDir(ctx, cmd.DAG, cmd.Status.DAGRunID)
		if err != nil {
			return fmt.Errorf("failed to generate queued catchup artifact directory: %w", err)
		}
		cmd.Status.ArchiveDir = artifactDir
	}

	attempt, err := s.findAttemptForStatus(ctx, cmd.Status, cmd.Root)
	if err != nil {
		return err
	}
	if err := attempt.Open(ctx); err != nil {
		return fmt.Errorf("failed to open queued catchup attempt: %w", err)
	}
	defer func() {
		_ = attempt.Close(ctx)
	}()

	if err := attempt.Write(ctx, *cmd.Status); err != nil {
		return fmt.Errorf("failed to persist queued catchup log file path: %w", err)
	}
	return nil
}

func (s *Service) findAttemptForStatus(ctx context.Context, status *exec.DAGRunStatus, root exec.DAGRunRef) (exec.DAGRunAttempt, error) {
	if root.Zero() {
		root = status.Root
	}
	if root.Zero() {
		root = status.DAGRun()
	}
	if root.ID != "" && root.ID != status.DAGRunID {
		return s.cfg.DAGRunStore.FindSubAttempt(ctx, root, status.DAGRunID)
	}
	return s.cfg.DAGRunStore.FindAttempt(ctx, status.DAGRun())
}
