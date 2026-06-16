// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"crypto/rand"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
)

// EvalBool evaluates the given value with the variables within the execution context
// and parses it as a boolean.
func EvalBool(ctx context.Context, value any) (bool, error) {
	return GetEnv(ctx).EvalBool(ctx, value)
}

// EvalObject recursively evaluates the string fields of the given object
// with the variables within the execution context.
func EvalObject[T any](ctx context.Context, obj T) (T, error) {
	env := GetEnv(ctx)
	resolver := resolverFromEnv(env)
	got, err := resolver.Object(ctx, obj, cmnvalue.WorkflowObjectField("object"))
	if err != nil {
		return obj, err
	}
	val, ok := got.(T)
	if !ok {
		return obj, fmt.Errorf("type assertion failed: expected %T, got %T", obj, got)
	}
	return val, nil
}

func resolverFromEnv(env Env) cmnvalue.Resolver {
	var consts cmnvalue.Values
	if env.DAG != nil {
		consts = cmnvalue.Values(env.DAG.Consts)
	}
	scope := cmnvalue.RuntimeScope{
		Consts: consts,
		Env:    env.Scope,
		Steps:  env.StepMap,
	}
	return cmnvalue.NewResolver(cmnvalue.StaticScope{Consts: consts}, scope)
}

func resolveRuntimeString(ctx context.Context, raw string, field cmnvalue.Field) (string, error) {
	return resolverFromEnv(GetEnv(ctx)).String(ctx, raw, field)
}

func resolveRuntimeObject(ctx context.Context, obj any, field cmnvalue.Field) (any, error) {
	return resolverFromEnv(GetEnv(ctx)).Object(ctx, obj, field)
}

func resolveRuntimeInt(ctx context.Context, raw string, field cmnvalue.Field) (int, error) {
	return resolverFromEnv(GetEnv(ctx)).Int(ctx, raw, field)
}

func resolveWithEnvScope(ctx context.Context, env Env, scope *cmnvalue.EnvScope, raw string, field cmnvalue.Field) (string, error) {
	copy := env
	copy.Scope = scope
	return resolverFromEnv(copy).String(ctx, raw, field)
}

// ValueResolver returns a semantic value resolver for the runtime environment in ctx.
func ValueResolver(ctx context.Context) cmnvalue.Resolver {
	return resolverFromEnv(GetEnv(ctx))
}

// ValueResolverWithScope returns a semantic value resolver using scope as the runtime env scope.
func ValueResolverWithScope(ctx context.Context, scope *cmnvalue.EnvScope) cmnvalue.Resolver {
	env := GetEnv(ctx)
	env.Scope = scope
	return resolverFromEnv(env)
}

// ResolveString resolves raw with the semantic field in the runtime environment.
func ResolveString(ctx context.Context, raw string, field cmnvalue.Field) (string, error) {
	return ValueResolver(ctx).String(ctx, raw, field)
}

// templateConfigEvalVariables clones the user env map and seeds omitted named DAG
// params with empty strings for template executor config evaluation.
func templateConfigEvalVariables(env Env) map[string]string {
	vars := env.UserEnvsMap()
	if env.DAG == nil || len(env.DAG.ParamDefs) == 0 {
		return vars
	}

	cloned := make(map[string]string, len(vars)+len(env.DAG.ParamDefs))
	maps.Copy(cloned, vars)

	for _, def := range env.DAG.ParamDefs {
		name := strings.TrimSpace(def.Name)
		if name == "" || isPositionalParamName(name) {
			continue
		}
		if _, ok := cloned[name]; ok {
			continue
		}
		cloned[name] = ""
	}

	return cloned
}

// isPositionalParamName reports whether a param name is a positional index rather
// than a named parameter.
func isPositionalParamName(name string) bool {
	_, err := strconv.Atoi(name)
	return err == nil
}

// GenerateSubDAGRunID generates a unique run ID based on the current DAG run ID, step name, and parameters.
func GenerateSubDAGRunID(ctx context.Context, params string, repeated bool) string {
	return GenerateSubDAGRunIDForTarget(ctx, "", params, repeated)
}

// GenerateSubDAGRunIDForTarget generates a unique run ID for a sub-DAG target.
// Including the target keeps deterministic IDs stable for retries while avoiding
// collisions when one parent step dispatches different child DAGs with identical params.
func GenerateSubDAGRunIDForTarget(ctx context.Context, dagName, params string, repeated bool) string {
	identity := params
	if dagName != "" {
		identity = dagName + "\x00" + params
	}

	if repeated {
		// If this is a repeated sub DAG run, we need to generate a unique ID with randomness
		// to avoid collisions with previous runs.
		randomBytes := make([]byte, 8)
		if _, err := rand.Read(randomBytes); err != nil {
			randomBytes = fmt.Appendf(nil, "%d", time.Now().UnixNano())
		}
		return stringutil.Base58EncodeSHA256(
			fmt.Sprintf("%s:%s:%s:%x", GetEnv(ctx).DAGRunID, GetEnv(ctx).Step.Name, identity, randomBytes),
		)
	}
	env := GetEnv(ctx)
	return stringutil.Base58EncodeSHA256(
		fmt.Sprintf("%s:%s:%s", env.DAGRunID, env.Step.Name, identity),
	)
}
