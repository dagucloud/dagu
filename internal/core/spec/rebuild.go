// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"context"
	"fmt"
	"maps"

	"github.com/dagucloud/dagu/v2/internal/cmn/buildenv"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// RebuildFromYAML restores the fields of dag that JSON serialization excludes by
// reloading its YamlData, and returns dag. Fields that survive serialization are
// preserved as-is. A dag without YamlData is returned unchanged.
//
// Callers must resolve the runtime environment before calling, so those values
// are visible to the rebuild. When paramsOverride is supplied its first element
// replaces the DAG's params for the reload.
//
// Env is drawn from the reloaded YAML and from the environment captured when the
// run was first built. A captured key overrides the YAML's declaration of it, so
// the rebuild reuses the original value rather than one the current process may
// resolve differently or fail to resolve at all. Keys the YAML does not declare
// are appended from the captured environment instead of being dropped.
func RebuildFromYAML(ctx context.Context, dag *ir.DAG, paramsOverride ...[]string) (*ir.DAG, error) {
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
	transportEnv := buildenv.FromMap(presolvedBuildEnv.Env)
	maps.Copy(buildEnvMap, presolvedBuildEnv.Env)

	params := dag.Params
	if len(paramsOverride) > 0 {
		params = paramsOverride[0]
	}
	loadOpts := []LoadOption{
		WithParams(params),
		SkipSchemaValidation(),
	}
	runtimeResolved := dag.RuntimeResolved || presolvedBuildEnv.RuntimeResolved
	if len(buildEnvMap) > 0 || runtimeResolved {
		loadOpts = append(loadOpts, WithBuildEnvSnapshot(buildenv.Snapshot{
			Env:             buildEnvMap,
			RuntimeResolved: runtimeResolved,
		}))
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

	dag.RestoreUnpersistedFrom(fresh)
	// Env is the one restored field that is merged rather than replaced, so it is
	// resolved after the wholesale copy.
	dag.Env = buildenv.AppendMissing(fresh.Env, loadedEnv, buildenv.FromMap(dag.PresolvedBuildEnv), transportEnv)

	ir.InitializeDefaults(dag)

	return dag, nil
}
