// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"context"
	"fmt"
	"strings"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec/types"
)

// loadVariables loads environment variables from strVariables and returns the
// resulting map of key->value without modifying the global OS environment.
//
// strVariables may be either a map[string]any or a []any containing maps and/or
// "key=value" strings; entries are collected in input order. For each pair, the
// value is optionally expanded with references to previously defined variables
// unless the BuildFlagNoEval option is set on ctx. The environment is passed via
// context to ensure thread-safety during concurrent DAG loading. The function
// returns a validation error if the input is malformed or a value fails to
// evaluate.
func loadVariables(ctx BuildContext, strVariables any) (map[string]string, error) {
	var pairs []pair
	switch a := strVariables.(type) {
	case map[string]any:
		if err := parseKeyValue(a, &pairs); err != nil {
			return nil, core.NewValidationError("env", a, err)
		}

	case []any:
		for _, v := range a {
			switch vv := v.(type) {
			case map[string]any:
				if err := parseKeyValue(vv, &pairs); err != nil {
					return nil, core.NewValidationError("env", v, err)
				}
			case string:
				key, val, found := strings.Cut(vv, "=")
				if !found {
					return nil, core.NewValidationError("env", &pairs, fmt.Errorf("%w: %s", ErrInvalidEnvValue, v))
				}
				pairs = append(pairs, pair{key: key, val: val})
			default:
				return nil, core.NewValidationError("env", &pairs, fmt.Errorf("%w: %s", ErrInvalidEnvValue, v))
			}
		}
	}

	return evaluatePairs(ctx, pairs)
}

// loadVariablesFromEnvValue loads environment variables from a types.EnvValue.
// This function converts the typed EnvValue entries to the expected format
// and processes them using the same logic as loadVariables without modifying
// the global OS environment.
func loadVariablesFromEnvValue(ctx BuildContext, env types.EnvValue) (map[string]string, error) {
	if env.IsZero() {
		return nil, nil
	}

	entries := env.Entries()
	pairs := make([]pair, len(entries))
	for i, entry := range entries {
		pairs[i] = pair{key: entry.Key, val: entry.Value}
	}

	return evaluatePairs(ctx, pairs)
}

// evaluatePairs evaluates a list of key-value pairs, expanding environment
// variables unless BuildFlagNoEval is set.
func evaluatePairs(ctx BuildContext, pairs []pair) (map[string]string, error) {
	vars := make(map[string]string, len(pairs))

	// Build base scope once outside the loop to reduce allocations.
	// New entries are chained immutably as each pair is evaluated.
	var scope *cmnvalue.EnvScope
	var evalCtx context.Context
	if !ctx.opts.Has(BuildFlagNoEval) {
		// Use the shared build scope (which includes resolved params)
		// instead of a fresh OS-only scope, so env: can reference ${param_name}.
		if ctx.envScope != nil && ctx.envScope.scope != nil {
			scope = ctx.envScope.scope
		} else {
			scope = cmnvalue.NewEnvScope(nil, true)
		}
		evalCtx = ctx.ctx
		if evalCtx == nil {
			evalCtx = context.Background()
		}
	}
	var consts map[string]any
	if ctx.envScope != nil {
		consts = ctx.envScope.consts
	}

	for _, p := range pairs {
		value := p.val

		if !ctx.opts.Has(BuildFlagNoEval) {
			if presolved, ok := ctx.opts.BuildEnv[p.key]; ok {
				value = presolved
				scope = scope.WithEntry(p.key, value, cmnvalue.EnvSourcePresolved)
				vars[p.key] = value
				continue
			}

			var err error
			resolver := cmnvalue.NewResolver(
				cmnvalue.StaticScope{Consts: cmnvalue.Values(consts)},
				cmnvalue.RuntimeScope{Consts: cmnvalue.Values(consts), Env: scope},
			)
			value, err = resolver.String(evalCtx, value, cmnvalue.DAGEnvField("env."+p.key))
			if err != nil {
				return nil, core.NewValidationError("env", p.val, fmt.Errorf("%w: %s", ErrInvalidEnvValue, p.val))
			}

			// Add evaluated value to scope for next iteration
			scope = scope.WithEntry(p.key, value, cmnvalue.EnvSourceDAGEnv)
		}

		vars[p.key] = value
	}

	return vars, nil
}

// collectRawPairs parses environment variable definitions from strVariables
// into raw "KEY=VALUE" strings without any evaluation or expansion.
// This is used for container env, where evaluation is deferred to runtime
// so that DAG-level env, params, and step outputs are available in scope.
func collectRawPairs(strVariables any) ([]string, error) {
	var pairs []pair
	switch a := strVariables.(type) {
	case map[string]any:
		if err := parseKeyValue(a, &pairs); err != nil {
			return nil, core.NewValidationError("env", a, err)
		}

	case []any:
		for _, v := range a {
			switch vv := v.(type) {
			case map[string]any:
				if err := parseKeyValue(vv, &pairs); err != nil {
					return nil, core.NewValidationError("env", v, err)
				}
			case string:
				key, _, found := strings.Cut(vv, "=")
				if !found {
					return nil, core.NewValidationError("env", &pairs, fmt.Errorf("%w: %s", ErrInvalidEnvValue, v))
				}
				pairs = append(pairs, pair{key: key, val: vv[len(key)+1:]})
			default:
				return nil, core.NewValidationError("env", &pairs, fmt.Errorf("%w: %s", ErrInvalidEnvValue, v))
			}
		}
	}

	if len(pairs) == 0 {
		return nil, nil
	}

	envs := make([]string, len(pairs))
	for i, p := range pairs {
		envs[i] = fmt.Sprintf("%s=%s", p.key, p.val)
	}
	return envs, nil
}

// pair represents a key-value pair.
type pair struct {
	key string
	val string
}

// parseKeyValue parse a key-value pair from a map and appends it to the pairs
// slice. Each entry in the map must have a string key and a string value.
func parseKeyValue(m map[string]any, pairs *[]pair) error {
	for key, v := range m {
		var val string
		switch v := v.(type) {
		case string:
			val = v
		default:
			val = fmt.Sprintf("%v", v)
		}

		*pairs = append(*pairs, pair{key: key, val: val})
	}
	return nil
}
