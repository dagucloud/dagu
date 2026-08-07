// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"context"
	"fmt"
	"maps"

	"github.com/dagucloud/dagu/v2/internal/cmn/buildenv"
	"github.com/dagucloud/dagu/v2/internal/core"
)

// RebuildFromYAML restores the fields of dag that JSON serialization excludes by
// reloading its YamlData, and returns dag. Fields that survive serialization are
// preserved as-is. A dag without YamlData is returned unchanged.
//
// Callers must load dotenv before calling, so dotenv values are visible to the
// rebuild. When paramsOverride is supplied its first element replaces the DAG's
// params for the reload.
//
// Env resolves first-wins: keys the reloaded YAML declares take precedence, and
// keys it cannot resolve fall back to the values captured when the run was first
// built rather than being dropped.
func RebuildFromYAML(ctx context.Context, dag *core.DAG, paramsOverride ...[]string) (*core.DAG, error) {
	if len(dag.YamlData) == 0 {
		return dag, nil
	}

	loadedEnv := append([]string{}, dag.Env...)
	buildEnvMap := buildenv.ToMap(dag.Env)
	if buildEnvMap == nil {
		buildEnvMap = make(map[string]string)
	}
	maps.Copy(buildEnvMap, dag.PresolvedBuildEnv)

	presolvedBuildEnv, err := buildenv.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load presolved build env: %w", err)
	}
	transportEnv := buildenv.FromMap(presolvedBuildEnv)
	maps.Copy(buildEnvMap, presolvedBuildEnv)

	params := dag.Params
	if len(paramsOverride) > 0 {
		params = paramsOverride[0]
	}
	loadOpts := []LoadOption{
		WithParams(params),
		SkipSchemaValidation(),
	}
	if len(buildEnvMap) > 0 {
		loadOpts = append(loadOpts, WithBuildEnv(buildEnvMap))
	}
	if len(dag.BaseConfigData) > 0 {
		loadOpts = append(loadOpts, WithBaseConfigContent(dag.BaseConfigData))
	}
	if dag.Name != "" {
		loadOpts = append(loadOpts, WithName(dag.Name))
	}

	fresh, err := LoadYAML(ctx, dag.YamlData, loadOpts...)
	if err != nil {
		return nil, err
	}

	dag.Env = buildenv.AppendMissing(fresh.Env, loadedEnv, buildenv.FromMap(dag.PresolvedBuildEnv), transportEnv)
	dag.Params = fresh.Params
	dag.ParamsJSON = fresh.ParamsJSON
	dag.SMTP = fresh.SMTP
	dag.SSH = fresh.SSH
	dag.S3 = fresh.S3
	dag.Redis = fresh.Redis
	dag.RegistryAuths = fresh.RegistryAuths
	dag.Harness = fresh.Harness
	dag.Harnesses = fresh.Harnesses
	dag.Kubernetes = fresh.Kubernetes
	dag.WorkingDirExplicit = fresh.WorkingDirExplicit

	core.InitializeDefaults(dag)

	return dag, nil
}
