// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package transport prepares resolved environment snapshots for subprocess transport.
package transport

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/cmn/buildenv"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtimeenv"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

// Options controls how a DAG source is reloaded while preparing its environment.
type Options struct {
	BaseConfig             string
	WorkspaceBaseConfigDir string
}

// Resolve returns the complete per-run environment without mutating dag.
func Resolve(ctx context.Context, dag *ir.DAG, params any, opts Options) (runtimeenv.Result, error) {
	if dag == nil {
		return runtimeenv.Result{}, nil
	}
	if canReuseCurrentEnv(dag, params) {
		return runtimeenv.Result{Env: append([]string{}, dag.Env...)}, nil
	}

	reloadOpts := spec.ResolveRuntimeParamsOptions{
		BaseConfig:             opts.BaseConfig,
		WorkspaceBaseConfigDir: opts.WorkspaceBaseConfigDir,
	}
	runtimeParams, err := resolveRuntimeParams(ctx, dag, params, reloadOpts)
	if err != nil {
		return runtimeenv.Result{}, err
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
		return resolved, err
	}

	loadedEnv := resolved.Env
	var additional []spec.LoadOption
	if env := buildenv.ToMap(loadedEnv); len(env) > 0 {
		additional = append(additional, spec.WithBuildEnvSnapshot(buildenv.Snapshot{
			Env:             env,
			RuntimeResolved: true,
		}))
	}
	presolvedEnv := buildenv.FromMap(dag.PresolvedBuildEnv)
	if !hasDAGSource(dag) {
		resolved.Env = buildenv.AppendMissing(dag.Env, loadedEnv, presolvedEnv)
		return resolved, nil
	}

	fresh, err := spec.ReloadRuntimeSnapshot(ctx, dag, params, reloadOpts, additional...)
	if err != nil {
		return runtimeenv.Result{}, err
	}
	resolved.Env = buildenv.AppendMissing(fresh.Env, loadedEnv, presolvedEnv)
	return resolved, nil
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

func resolveRuntimeParams(
	ctx context.Context,
	dag *ir.DAG,
	params any,
	opts spec.ResolveRuntimeParamsOptions,
) ([]string, error) {
	if !hasDAGSource(dag) {
		return append([]string(nil), dag.Params...), nil
	}
	fresh, err := spec.ReloadRuntimeSnapshot(ctx, dag, params, opts)
	if err != nil {
		return nil, err
	}
	return append([]string(nil), fresh.Params...), nil
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
