// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"context"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/buildenv"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtimeenv"
)

// ResolveEnvOptions controls how a DAG is reloaded to recover resolved env
// values for subprocess launchers.
type ResolveEnvOptions struct {
	BaseConfig             string
	WorkspaceBaseConfigDir string
}

// ResolveRuntimeEnvResult contains resolved entries and non-fatal load warnings.
type ResolveRuntimeEnvResult struct {
	Env           []string
	BuildWarnings []string
}

// QuoteRuntimeParams quotes persisted params so values containing spaces survive
// re-parsing when a DAG is rebuilt from status metadata.
func QuoteRuntimeParams(params []string, paramDefs []ir.ParamDef) []string {
	positionalKeys := positionalParamKeys(paramDefs)
	quoted := make([]string, len(params))
	for i, p := range params {
		if k, v, ok := strings.Cut(p, "="); ok {
			if _, isPositional := positionalKeys[k]; isPositional {
				quoted[i] = strconv.Quote(v)
				continue
			}
			quoted[i] = k + "=" + strconv.Quote(v)
		} else {
			quoted[i] = strconv.Quote(p)
		}
	}
	return quoted
}

// ResolveRuntimeEnv returns the complete per-run environment without mutating dag.
func ResolveRuntimeEnv(ctx context.Context, dag *ir.DAG, params any, opts ResolveEnvOptions) (ResolveRuntimeEnvResult, error) {
	if dag == nil {
		return ResolveRuntimeEnvResult{}, nil
	}
	if canReuseCurrentEnv(dag, params) {
		return ResolveRuntimeEnvResult{Env: append([]string{}, dag.Env...)}, nil
	}

	loadOpts, err := runtimeParamLoadOptions(dag, params, ResolveRuntimeParamsOptions(opts))
	if err != nil {
		return ResolveRuntimeEnvResult{}, err
	}
	runtimeParams, err := resolveRuntimeParamsForEnv(ctx, dag, loadOpts)
	if err != nil {
		return ResolveRuntimeEnvResult{}, err
	}

	cloned := dag.Clone()
	cloned.Params = runtimeParams
	cloned.RuntimeResolved = false
	if shouldRecomputeEnv(dag, params) {
		// Recompute DAG/base-config env entries when params or raw source-backed
		// metadata can affect build-time env resolution.
		cloned.Env = nil
	} else {
		cloned.Env = append([]string(nil), cloned.Env...)
	}
	resolved, err := runtimeenv.Resolve(ctx, cloned)
	if err != nil {
		return ResolveRuntimeEnvResult{
			Env:           resolved.Env,
			BuildWarnings: resolved.Warnings,
		}, err
	}
	loadedEnv := resolved.Env
	buildEnv := buildenv.ToMap(loadedEnv)
	if len(buildEnv) > 0 {
		loadOpts = append(loadOpts, WithBuildEnvSnapshot(buildenv.Snapshot{
			Env:             buildEnv,
			RuntimeResolved: true,
		}))
	}
	presolvedEnv := buildenv.FromMap(dag.PresolvedBuildEnv)

	switch {
	case len(dag.YamlData) > 0:
		fresh, err := LoadYAML(ctx, dag.YamlData, loadOpts...)
		if err != nil {
			return ResolveRuntimeEnvResult{}, err
		}
		return ResolveRuntimeEnvResult{
			Env:           buildenv.AppendMissing(fresh.Env, loadedEnv, presolvedEnv),
			BuildWarnings: resolved.Warnings,
		}, nil

	case dag.Location != "":
		fresh, err := Load(ctx, dag.Location, loadOpts...)
		if err != nil {
			return ResolveRuntimeEnvResult{}, err
		}
		return ResolveRuntimeEnvResult{
			Env:           buildenv.AppendMissing(fresh.Env, loadedEnv, presolvedEnv),
			BuildWarnings: resolved.Warnings,
		}, nil

	case dag.SourceFile != "":
		fresh, err := Load(ctx, dag.SourceFile, loadOpts...)
		if err != nil {
			return ResolveRuntimeEnvResult{}, err
		}
		return ResolveRuntimeEnvResult{
			Env:           buildenv.AppendMissing(fresh.Env, loadedEnv, presolvedEnv),
			BuildWarnings: resolved.Warnings,
		}, nil

	default:
		return ResolveRuntimeEnvResult{
			Env:           buildenv.AppendMissing(dag.Env, loadedEnv, presolvedEnv),
			BuildWarnings: resolved.Warnings,
		}, nil
	}
}

func canReuseCurrentEnv(dag *ir.DAG, params any) bool {
	return !hasRuntimeParams(params) && dag.RuntimeResolved
}

func shouldRecomputeEnv(dag *ir.DAG, params any) bool {
	return hasRuntimeParams(params) || (hasDAGSource(dag) && !dag.EnvEvaluated)
}

func hasDAGSource(dag *ir.DAG) bool {
	return len(dag.YamlData) > 0 || dag.Location != "" || dag.SourceFile != ""
}

func resolveRuntimeParamsForEnv(ctx context.Context, dag *ir.DAG, loadOpts []LoadOption) ([]string, error) {
	switch {
	case len(dag.YamlData) > 0:
		fresh, err := LoadYAML(ctx, dag.YamlData, loadOpts...)
		if err != nil {
			return nil, err
		}
		return append([]string(nil), fresh.Params...), nil
	case dag.Location != "":
		fresh, err := Load(ctx, dag.Location, loadOpts...)
		if err != nil {
			return nil, err
		}
		return append([]string(nil), fresh.Params...), nil
	case dag.SourceFile != "":
		fresh, err := Load(ctx, dag.SourceFile, loadOpts...)
		if err != nil {
			return nil, err
		}
		return append([]string(nil), fresh.Params...), nil
	default:
		return append([]string(nil), dag.Params...), nil
	}
}

func positionalParamKeys(paramDefs []ir.ParamDef) map[string]struct{} {
	if len(paramDefs) == 0 {
		return nil
	}

	keys := make(map[string]struct{})
	position := 1
	for _, def := range paramDefs {
		if def.Name != "" {
			continue
		}
		keys[strconv.Itoa(position)] = struct{}{}
		position++
	}

	return keys
}

func hasRuntimeParams(params any) bool {
	switch value := params.(type) {
	case nil:
		return false
	case string:
		return value != ""
	case []string:
		return len(value) > 0
	default:
		return true
	}
}
