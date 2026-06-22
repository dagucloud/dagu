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
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
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
	var params cmnvalue.Values
	var paramDeclarations cmnvalue.Values
	if env.DAG != nil {
		consts = cmnvalue.Values(env.DAG.Consts)
		params = env.DAG.ParamValues()
		paramDeclarations = env.DAG.ParamDeclarations()
	}
	scope := cmnvalue.RuntimeScope{
		Consts:         consts,
		Params:         params,
		Env:            env.Scope,
		Steps:          env.StepMap,
		Foreach:        env.Foreach,
		BuiltinContext: builtinContextFromEnv(env),
	}
	return cmnvalue.NewResolver(cmnvalue.StaticScope{Consts: consts, Params: paramDeclarations}, scope)
}

func builtinContextFromEnv(env Env) cmnvalue.BuiltinContext {
	return builtinContextFromDAGContext(env.Context, env.Scope, env.Step)
}

func builtinContextFromDAGContext(rCtx Context, scope *cmnvalue.EnvScope, step core.Step) cmnvalue.BuiltinContext {
	values := make(map[string]string)
	addBuiltinContextValue(values, "context.dag.name", dagName(rCtx))
	addBuiltinContextValue(values, "context.run.id", rCtx.DAGRunID)
	addBuiltinContextValue(values, "context.attempt.started_at", rCtx.RunStartedAt)
	addBuiltinContextValue(values, "context.run.scheduled_at", rCtx.ScheduleTime)
	if rootDAGRunContextAvailable(rCtx) {
		addBuiltinContextValue(values, "context.run.root_name", rCtx.RootDAGRun.Name)
		addBuiltinContextValue(values, "context.run.root_id", rCtx.RootDAGRun.ID)
	}
	addBuiltinContextValue(values, "context.attempt.id", rCtx.AttemptID)
	addBuiltinContextValue(values, "context.step.id", step.ID)
	addBuiltinContextValue(values, "context.step.name", step.Name)
	addBuiltinContextValue(values, "context.trigger.type", rCtx.TriggerType.String())
	addBuiltinContextValue(values, "context.trigger.actor", rCtx.TriggerActor)
	addBuiltinContextEnvValue(values, "context.run.status", scope, exec.EnvKeyDAGRunStatus)
	addBuiltinContextEnvValue(values, "context.paths.log_file", scope, exec.EnvKeyDAGRunLogFile)
	addBuiltinContextEnvValue(values, "context.paths.work_dir", scope, exec.EnvKeyDAGRunWorkDir)
	addBuiltinContextEnvValue(values, "context.paths.artifacts_dir", scope, exec.EnvKeyDAGRunArtifactsDir)
	addBuiltinContextEnvValue(values, "context.paths.docs_dir", scope, exec.EnvKeyDAGDocsDir)
	addBuiltinContextEnvValue(values, "context.paths.step_stdout_file", scope, exec.EnvKeyDAGRunStepStdoutFile)
	addBuiltinContextEnvValue(values, "context.paths.step_stderr_file", scope, exec.EnvKeyDAGRunStepStderrFile)
	addBuiltinContextEnvValue(values, "context.paths.step_output_file", scope, exec.EnvKeyDAGUOutputFile)
	addBuiltinContextValue(values, "context.profile.name", rCtx.ProfileName)
	addBuiltinContextValue(values, "context.profile.resolved_at", rCtx.ProfileResolvedAt)
	addBuiltinContextEnvValue(values, "context.pushback.iteration", scope, exec.EnvKeyDAGPushBackIteration)
	addBuiltinContextEnvValue(values, "context.pushback.previous_stdout_file", scope, exec.EnvKeyDAGPushBackPreviousStdoutFile)
	return cmnvalue.NewBuiltinContext(values)
}

func dagName(rCtx Context) string {
	if rCtx.DAG == nil {
		return ""
	}
	return rCtx.DAG.Name
}

func rootDAGRunContextAvailable(rCtx Context) bool {
	if rCtx.RootDAGRun.Zero() {
		return false
	}
	if rCtx.DAG != nil && rCtx.RootDAGRun.Name == rCtx.DAG.Name && rCtx.RootDAGRun.ID == rCtx.DAGRunID {
		return false
	}
	return true
}

func addBuiltinContextValue(values map[string]string, path string, value string) {
	if value == "" {
		return
	}
	values[path] = value
}

func addBuiltinContextEnvValue(values map[string]string, path string, scope *cmnvalue.EnvScope, key string) {
	if scope == nil {
		return
	}
	value, ok := scope.Get(key)
	if !ok {
		return
	}
	addBuiltinContextValue(values, path, value)
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
