// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runctx

import (
	"context"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// buildManagedDAGRunEnvs returns the environment variables Dagu generates for a
// dag-run. Keys whose value is unavailable are omitted rather than set empty.
func buildManagedDAGRunEnvs(
	ctx context.Context,
	dag *ir.DAG,
	dagRunID string,
	logFile string,
	options *contextOptions,
) map[string]string {
	envs := map[string]string{
		runenv.EnvKeyDAGRunLogFile: logFile,
		runenv.EnvKeyDAGRunID:      dagRunID,
		runenv.EnvKeyDAGName:       dag.Name,
	}
	if docsDir := dagDocsDir(ctx, dag); docsDir != "" {
		envs[runenv.EnvKeyDAGDocsDir] = docsDir
	}
	if options.workDir != "" {
		envs[runenv.EnvKeyDAGRunWorkDir] = options.workDir
	}
	if options.artifactDir != "" {
		envs[runenv.EnvKeyDAGRunArtifactsDir] = options.artifactDir
	}
	if dag.ParamsJSON != "" {
		envs[runenv.EnvKeyDAGParamsJSON] = dag.ParamsJSON
		envs[runenv.EnvKeyDAGParamsJSONCompat] = dag.ParamsJSON
	}
	return envs
}

// dagDocsDir returns the documents directory for the DAG, or an empty string
// when no documents root is configured.
func dagDocsDir(ctx context.Context, dag *ir.DAG) string {
	cfg := config.GetConfig(ctx)
	if cfg.Paths.DocsDir == "" {
		return ""
	}
	if workspaceName, ok := workspace.WorkspaceNameFromLabels(dag.Labels); ok {
		return filepath.Join(cfg.Paths.DocsDir, workspaceName, dag.Name)
	}
	return filepath.Join(cfg.Paths.DocsDir, dag.Name)
}
