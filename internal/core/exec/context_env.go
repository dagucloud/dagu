// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec

import (
	"context"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/core"
)

// buildManagedDAGRunEnvs returns the environment variables Dagu generates for a
// dag-run. Keys whose value is unavailable are omitted rather than set empty.
func buildManagedDAGRunEnvs(
	ctx context.Context,
	dag *core.DAG,
	dagRunID string,
	logFile string,
	options *contextOptions,
) map[string]string {
	envs := map[string]string{
		EnvKeyDAGRunLogFile: logFile,
		EnvKeyDAGRunID:      dagRunID,
		EnvKeyDAGName:       dag.Name,
	}
	if docsDir := dagDocsDir(ctx, dag); docsDir != "" {
		envs[EnvKeyDAGDocsDir] = docsDir
	}
	if options.workDir != "" {
		envs[EnvKeyDAGRunWorkDir] = options.workDir
	}
	if options.artifactDir != "" {
		envs[EnvKeyDAGRunArtifactsDir] = options.artifactDir
	}
	if dag.ParamsJSON != "" {
		envs[EnvKeyDAGParamsJSON] = dag.ParamsJSON
		envs[EnvKeyDAGParamsJSONCompat] = dag.ParamsJSON
	}
	return envs
}

// dagDocsDir returns the documents directory for the DAG, or an empty string
// when no documents root is configured.
func dagDocsDir(ctx context.Context, dag *core.DAG) string {
	cfg := config.GetConfig(ctx)
	if cfg.Paths.DocsDir == "" {
		return ""
	}
	if workspaceName, ok := WorkspaceNameFromLabels(dag.Labels); ok {
		return filepath.Join(cfg.Paths.DocsDir, workspaceName, dag.Name)
	}
	return filepath.Join(cfg.Paths.DocsDir, dag.Name)
}
