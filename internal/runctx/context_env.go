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
	if wikiDir := dagWikiDir(ctx, dag); wikiDir != "" {
		envs[runenv.EnvKeyDAGWikiDir] = wikiDir
		envs[runenv.EnvKeyDAGDocsDir] = wikiDir
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

// dagWikiDir returns the Wiki directory for the DAG, or an empty string
// when no Wiki root is configured.
func dagWikiDir(ctx context.Context, dag *ir.DAG) string {
	cfg := config.GetConfig(ctx)
	if cfg.Paths.WikiDir == "" {
		return ""
	}
	if workspaceName, ok := workspace.WorkspaceNameFromLabels(dag.Labels); ok {
		return filepath.Join(cfg.Paths.WikiDir, workspaceName, dag.Name)
	}
	return filepath.Join(cfg.Paths.WikiDir, dag.Name)
}
