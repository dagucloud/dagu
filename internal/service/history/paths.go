// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package history

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/logpath"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

func writeFailedStatus(ctx context.Context, attempt exec.DAGRunAttempt, status exec.DAGRunStatus, openErr string) error {
	if err := attempt.Open(ctx); err != nil {
		return fmt.Errorf("%s: %w", openErr, err)
	}
	defer func() {
		_ = attempt.Close(ctx)
	}()

	if err := attempt.Write(ctx, status); err != nil {
		return fmt.Errorf("failed to write failed status: %w", err)
	}
	return nil
}

func (s *Service) localArtifactDir(ctx context.Context, dag *core.DAG, dagRunID string) (string, error) {
	if !dag.ArtifactsEnabled() {
		return "", nil
	}
	return logpath.GenerateDir(ctx, s.cfg.ArtifactBaseDir, dag.Artifacts.Dir, dag.Name, dagRunID)
}

func (s *Service) failurePaths(
	ctx context.Context,
	dag *core.DAG,
	dagRunID string,
	logWarning string,
	artifactWarning string,
) (string, string) {
	logFile, logErr := logpath.Generate(ctx, s.cfg.LogBaseDir, dag.LogDir, dag.Name, dagRunID)
	if logErr != nil {
		logger.Warn(ctx, logWarning,
			tag.Error(logErr),
			tag.DAG(dag.Name),
			tag.RunID(dagRunID),
		)
	}

	archiveDir, archiveErr := s.localArtifactDir(ctx, dag, dagRunID)
	if archiveErr != nil {
		logger.Warn(ctx, artifactWarning,
			tag.Error(archiveErr),
			tag.DAG(dag.Name),
			tag.RunID(dagRunID),
		)
	}
	return logFile, archiveDir
}
