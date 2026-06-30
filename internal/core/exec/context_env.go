// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec

import (
	"context"

	"github.com/dagucloud/dagu/internal/core"
)

type managedDAGRunEnv struct {
	key   string
	value func(context.Context, *core.DAG, string, string, *contextOptions) (string, bool)
}

var managedDAGRunEnvs = []managedDAGRunEnv{
	{
		key: EnvKeyDAGRunLogFile,
		value: func(_ context.Context, _ *core.DAG, _ string, logFile string, _ *contextOptions) (string, bool) {
			return logFile, true
		},
	},
	{
		key: EnvKeyDAGRunID,
		value: func(_ context.Context, _ *core.DAG, dagRunID string, _ string, _ *contextOptions) (string, bool) {
			return dagRunID, true
		},
	},
	{
		key: EnvKeyDAGName,
		value: func(_ context.Context, dag *core.DAG, _ string, _ string, _ *contextOptions) (string, bool) {
			return dag.Name, true
		},
	},
	{
		key: EnvKeyDAGRunWorkDir,
		value: func(_ context.Context, _ *core.DAG, _ string, _ string, options *contextOptions) (string, bool) {
			if options.workDir == "" {
				return "", false
			}
			return options.workDir, true
		},
	},
	{
		key: EnvKeyDAGRunArtifactsDir,
		value: func(_ context.Context, _ *core.DAG, _ string, _ string, options *contextOptions) (string, bool) {
			if options.artifactDir == "" {
				return "", false
			}
			return options.artifactDir, true
		},
	},
	{
		key: EnvKeyDAGParamsJSONCompat,
		value: func(_ context.Context, dag *core.DAG, _ string, _ string, _ *contextOptions) (string, bool) {
			if dag.ParamsJSON == "" {
				return "", false
			}
			return dag.ParamsJSON, true
		},
	},
	{
		key: EnvKeyDAGParamsJSON,
		value: func(_ context.Context, dag *core.DAG, _ string, _ string, _ *contextOptions) (string, bool) {
			if dag.ParamsJSON == "" {
				return "", false
			}
			return dag.ParamsJSON, true
		},
	},
}

func buildManagedDAGRunEnvs(
	ctx context.Context,
	dag *core.DAG,
	dagRunID string,
	logFile string,
	options *contextOptions,
) map[string]string {
	envs := make(map[string]string, len(managedDAGRunEnvs))
	for _, env := range managedDAGRunEnvs {
		value, ok := env.value(ctx, dag, dagRunID, logFile, options)
		if ok {
			envs[env.key] = value
		}
	}
	return envs
}
