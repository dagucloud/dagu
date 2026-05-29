// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"context"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/internal/cmn/buildenv"
	"github.com/dagucloud/dagu/internal/core"
)

// ResolveEnvOptions controls how a DAG is reloaded to recover resolved env
// values for subprocess launchers.
type ResolveEnvOptions struct {
	BaseConfig string
}

// QuoteRuntimeParams quotes persisted params so values containing spaces survive
// re-parsing when a DAG is rebuilt from status metadata.
func QuoteRuntimeParams(params []string, paramDefs []core.ParamDef) []string {
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

// ResolveEnv rebuilds the DAG env from source when the current DAG snapshot no
// longer carries resolved env entries (for example when restored from dag.json).
func ResolveEnv(ctx context.Context, dag *core.DAG, params any, opts ResolveEnvOptions) ([]string, error) {
	if dag == nil {
		return nil, nil
	}
	if !hasRuntimeParams(params) && len(dag.Env) > 0 {
		return append([]string{}, dag.Env...), nil
	}

	loadOpts, err := runtimeParamLoadOptions(dag, params, ResolveRuntimeParamsOptions(opts))
	if err != nil {
		return nil, err
	}

	cloned := dag.Clone()
	if hasRuntimeParams(params) {
		// Recompute DAG/base-config env entries for the new runtime params instead
		// of short-circuiting to whatever happened to be on the current snapshot.
		cloned.Env = nil
	}
	cloned.LoadDotEnv(ctx)
	loadedEnv := append([]string{}, cloned.Env...)
	buildEnv := buildEnvMap(loadedEnv)
	if len(buildEnv) > 0 {
		loadOpts = append(loadOpts, WithBuildEnv(buildEnv))
	}
	presolvedEnv := buildenv.FromMap(dag.PresolvedBuildEnv)

	switch {
	case len(dag.YamlData) > 0:
		fresh, err := LoadYAML(ctx, dag.YamlData, loadOpts...)
		if err != nil {
			return nil, err
		}
		return buildenv.AppendMissing(fresh.Env, loadedEnv, presolvedEnv), nil

	case dag.Location != "":
		fresh, err := Load(ctx, dag.Location, loadOpts...)
		if err != nil {
			return nil, err
		}
		return buildenv.AppendMissing(fresh.Env, loadedEnv, presolvedEnv), nil

	default:
		return buildenv.AppendMissing(dag.Env, loadedEnv, presolvedEnv), nil
	}
}

func positionalParamKeys(paramDefs []core.ParamDef) map[string]struct{} {
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

func buildEnvMap(env []string) map[string]string {
	if len(env) == 0 {
		return nil
	}

	buildEnv := make(map[string]string)
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if !ok || key == "" {
			continue
		}
		buildEnv[key] = value
	}
	if len(buildEnv) == 0 {
		return nil
	}
	return buildEnv
}
