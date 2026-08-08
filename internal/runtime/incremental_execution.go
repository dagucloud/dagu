// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/incremental"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

func (r *Runner) startIncrementalSession(ctx context.Context, plan *Plan, node *Node) (context.Context, *incremental.Session, error) {
	dag := GetDAGContext(ctx).DAG
	if dag == nil || dag.Type != ir.TypeIncremental {
		return ctx, nil, nil
	}

	env := GetEnv(ctx)
	environment := make(map[string]string)
	runWorkDir := ""
	if env.Scope != nil {
		environment = env.Scope.ToMap()
		runWorkDir, _ = env.Scope.Get(exec.EnvKeyDAGRunWorkDir)
	}
	shell, err := env.ResolveShell(ctx)
	if err != nil {
		return ctx, nil, err
	}
	controlTokens := make(map[string]string)
	controlDependencyRan := false
	deferred := false
	for _, dependencyID := range plan.Dependencies(node.ID()) {
		dependency := plan.GetNode(dependencyID)
		state := dependency.State()
		if plan.IsInferredDependency(dependencyID, node.ID()) {
			if r.dry && (state.Incremental == nil || state.Incremental.Decision != exec.IncrementalDecisionReuse) {
				deferred = true
			}
			continue
		}
		if state.Incremental != nil && state.Incremental.Fingerprint != "" {
			controlTokens[dependency.Name()] = state.Incremental.Fingerprint
		}
		if state.Incremental != nil && state.Incremental.Decision == exec.IncrementalDecisionReuse {
			continue
		}
		controlDependencyRan = true
	}
	session, err := incremental.Prepare(ctx, r.materializations, incremental.PrepareRequest{
		DAG:                  dag,
		Step:                 node.Step(),
		DAGRunID:             r.dagRunID,
		AttemptID:            GetDAGContext(ctx).AttemptID,
		WorkingDir:           env.WorkingDir,
		RunWorkDir:           runWorkDir,
		Shell:                shell,
		Environment:          environment,
		HasSecrets:           env.Scope != nil && len(env.Scope.AllSecrets()) > 0,
		NoReuse:              r.noReuse,
		Dry:                  r.dry,
		Deferred:             deferred,
		ControlDependencyRan: controlDependencyRan,
		ControlTokens:        controlTokens,
	})
	if session != nil {
		node.setIncremental(session.Metadata())
		var pathErr error
		ctx, pathErr = withIncrementalPaths(ctx, node, session.InputPaths(), nil, !session.HasPathOutput())
		if pathErr != nil {
			_ = session.Close("")
			return ctx, nil, pathErr
		}
		if err != nil {
			return ctx, session, err
		}
	}
	return ctx, session, err
}

func markIncrementalPrecondition(
	session *incremental.Session,
	node *Node,
	reason exec.IncrementalReason,
	detail string,
	progressCh chan *Node,
) {
	if session == nil {
		return
	}
	metadata := session.Metadata()
	metadata.Decision = exec.IncrementalDecisionNone
	metadata.Phase = exec.IncrementalPhasePrecondition
	metadata.Reason = reason
	metadata.Detail = detail
	node.setIncremental(metadata)
	if progressCh != nil {
		progressCh <- node
	}
}

func (r *Runner) evaluateIncrementalNode(
	ctx context.Context,
	node *Node,
	session *incremental.Session,
	reportPreparedNode func(),
) bool {
	if session == nil {
		return false
	}
	var err error
	if session.Metadata().Decision != exec.IncrementalDecisionAlways {
		var resolvedStep ir.Step
		var environment map[string]string
		resolvedStep, environment, err = resolveIncrementalRecipe(ctx, node.Step())
		if err == nil {
			session.SetResolvedRecipe(resolvedStep, environment)
		}
	}
	if err == nil {
		err = session.Evaluate(ctx)
	}
	if err != nil {
		node.setIncremental(session.Metadata())
		r.setLastError(err)
		node.MarkError(err)
		node.SetStatus(ir.NodeFailed)
		reportPreparedNode()
		return true
	}
	node.setIncremental(session.Metadata())
	if r.dry {
		node.IncDoneCount()
		node.SetStatus(ir.NodeSucceeded)
		reportPreparedNode()
		return true
	}
	if !session.Reused() {
		return false
	}
	if err := publishIncrementalOutputs(ctx, node, session.PublishedOutputs()); err != nil {
		r.setLastError(err)
		node.MarkError(err)
		node.SetStatus(ir.NodeFailed)
		reportPreparedNode()
		return true
	}
	node.IncDoneCount()
	node.SetStatus(ir.NodeSucceeded)
	reportPreparedNode()
	return true
}

func resolveIncrementalRecipe(ctx context.Context, step ir.Step) (ir.Step, map[string]string, error) {
	env := GetEnv(ctx)
	inputs := make(map[string]string, len(step.Inputs))
	for _, input := range step.Inputs {
		inputs[input.Name] = input.Path
	}
	outputs := make(map[string]string, len(step.Outputs))
	for _, output := range step.Outputs {
		if output.Path != "" {
			// Recipe resolution keeps the logical output binding stable across
			// attempts whose physical staging paths are necessarily different.
			outputs[output.Name] = "${outputs." + output.Name + "}"
		}
	}
	env.Inputs = cmnvalue.ValuesFromStrings(inputs)
	env.Outputs = cmnvalue.ValuesFromStrings(outputs)
	if err := addResolvedEnvVars(ctx, &env, step.Env, "env.", cmnvalue.StepEnvField); err != nil {
		return ir.Step{}, nil, err
	}

	resolvedCtx := WithEnv(ctx, env)
	resolvedStep, err := resolveStepCommandArgs(resolvedCtx, step)
	if err != nil {
		return ir.Step{}, nil, err
	}
	config, err := evalExecutorConfig(resolvedCtx, resolvedStep)
	if err != nil {
		return ir.Step{}, nil, fmt.Errorf("failed to evaluate step configuration: %w", err)
	}
	resolvedStep.ExecutorConfig.Config = config
	if resolvedStep.Script != "" {
		resolvedStep.Script, err = resolveRuntimeString(resolvedCtx, resolvedStep.Script, scriptField(resolvedCtx, resolvedStep))
		if err != nil {
			return ir.Step{}, nil, fmt.Errorf("failed to eval script: %w", err)
		}
	}
	return resolvedStep, env.Scope.ToMap(), nil
}

func prepareIncrementalAttempt(
	ctx context.Context,
	node *Node,
	session *incremental.Session,
	declaredStep ir.Step,
) (context.Context, string, error) {
	if session == nil || !session.HasPathOutput() {
		return ctx, "", nil
	}
	outputs, stagingPath, err := session.NewAttempt(node.GetRetryCount())
	if err != nil {
		return ctx, "", err
	}
	node.resetForIncrementalAttempt(declaredStep)
	attemptCtx, err := withIncrementalPaths(ctx, node, session.InputPaths(), outputs, true)
	if err != nil {
		return ctx, stagingPath, err
	}
	return attemptCtx, stagingPath, nil
}

func commitIncrementalAttempt(
	ctx context.Context,
	node *Node,
	session *incremental.Session,
	stagingPath string,
) (bool, error) {
	if session == nil || !session.HasPathOutput() {
		return false, nil
	}
	if err := session.Commit(ctx, stagingPath); err != nil {
		node.setIncremental(session.Metadata())
		return false, err
	}
	node.setIncremental(session.Metadata())
	return true, publishIncrementalOutputs(ctx, node, session.PublishedOutputs())
}

func withIncrementalPaths(
	ctx context.Context,
	node *Node,
	inputs, outputs map[string]string,
	resolveOutputs bool,
) (context.Context, error) {
	env := GetEnv(ctx)
	env.Inputs = cmnvalue.ValuesFromStrings(inputs)
	env.Outputs = cmnvalue.ValuesFromStrings(outputs)
	stepEnv := node.Step().Env
	if !resolveOutputs {
		stepEnv = environmentWithoutAttemptOutputs(stepEnv)
	}
	if err := addResolvedEnvVars(ctx, &env, stepEnv, "env.", cmnvalue.StepEnvField); err != nil {
		return ctx, err
	}
	return WithEnv(ctx, env), nil
}

func publishIncrementalOutputs(ctx context.Context, node *Node, outputs map[string]string) error {
	if len(outputs) == 0 {
		return nil
	}
	merged := make(map[string]string)
	if raw := node.State().StepOutputsValue; raw != nil && *raw != "" {
		if err := json.Unmarshal([]byte(*raw), &merged); err != nil {
			return fmt.Errorf("decode step outputs before publishing materialization: %w", err)
		}
	}
	maps.Copy(merged, outputs)
	serialized, err := serializeDeclaredStepOutputs(ctx, merged)
	if err != nil {
		return err
	}
	node.setStepOutputsValue(serialized)
	return nil
}
