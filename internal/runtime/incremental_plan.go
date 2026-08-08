// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/incremental"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runctx"
)

func prepareIncrementalPlan(ctx context.Context, plan *Plan) error {
	dag := GetDAGContext(ctx).DAG
	if dag == nil || dag.Type != ir.TypeIncremental {
		return nil
	}
	baseEnv, err := NewEnvWithError(ctx, ir.Step{})
	if err != nil {
		return err
	}
	base := incrementalDeclaredPathBase(dag, baseEnv.WorkingDir)
	pathKeys := incremental.NewPathKeyResolver()
	producers := make(map[string]string)
	declaredPaths := make(map[string]string)

	for _, node := range plan.Nodes() {
		step := node.Step()
		env, err := NewPlanEnvForNodeWithError(ctx, node, plan)
		if err != nil {
			return err
		}
		resolver := resolverFromEnv(env)
		for idx := range step.Inputs {
			resolved, err := resolver.String(ctx, step.Inputs[idx].Path, cmnvalue.WorkflowField(fmt.Sprintf("steps.%s.inputs.%s.path", step.Name, step.Inputs[idx].Name)))
			if err != nil {
				return err
			}
			if cmnvalue.HasValueReference(resolved) {
				return fmt.Errorf("step %s input %s path must resolve before execution", step.Name, step.Inputs[idx].Name)
			}
			step.Inputs[idx].Path, err = incremental.ResolvePath(resolved, base, false)
			if err != nil {
				return fmt.Errorf("step %s input %s: %w", step.Name, step.Inputs[idx].Name, err)
			}
			declaredPaths[pathKeys.ComparisonKey(step.Inputs[idx].Path)] = fmt.Sprintf("incremental input %s declared by %s", step.Inputs[idx].Name, step.Name)
		}
		for idx := range step.Outputs {
			if step.Outputs[idx].Path == "" {
				continue
			}
			resolved, err := resolver.String(ctx, step.Outputs[idx].Path, cmnvalue.WorkflowField(fmt.Sprintf("steps.%s.outputs.%s.path", step.Name, step.Outputs[idx].Name)))
			if err != nil {
				return err
			}
			if cmnvalue.HasValueReference(resolved) {
				return fmt.Errorf("step %s output %s path must resolve before execution", step.Name, step.Outputs[idx].Name)
			}
			step.Outputs[idx].Path, err = incremental.ResolvePath(resolved, base, true)
			if err != nil {
				return fmt.Errorf("step %s output %s: %w", step.Name, step.Outputs[idx].Name, err)
			}
			key := pathKeys.ComparisonKey(step.Outputs[idx].Path)
			if previous, exists := producers[key]; exists {
				return fmt.Errorf("incremental output path %s has multiple producers: %s and %s", step.Outputs[idx].Path, previous, step.Name)
			}
			producers[key] = step.Name
			declaredPaths[key] = fmt.Sprintf("incremental output produced by %s", step.Name)
		}
		for _, input := range step.Inputs {
			for _, output := range step.Outputs {
				if output.Path != "" && pathKeys.ComparisonKey(input.Path) == pathKeys.ComparisonKey(output.Path) {
					return fmt.Errorf("step %s declares the same path as input and output: %s", step.Name, input.Path)
				}
			}
		}
		node.SetStep(step)
	}

	for _, node := range plan.Nodes() {
		step := node.Step()
		env, err := NewPlanEnvForNodeWithError(ctx, node, plan)
		if err != nil {
			return err
		}
		if err := validateIncrementalRedirectAliases(ctx, step, env, base, declaredPaths, pathKeys, true); err != nil {
			return err
		}

		for _, input := range node.Step().Inputs {
			if producer, ok := producers[pathKeys.ComparisonKey(input.Path)]; ok {
				if err := plan.AddInferredDependency(producer, node.Name()); err != nil {
					return fmt.Errorf("infer dependency %s -> %s: %w", producer, node.Name(), err)
				}
			}
		}
	}
	return nil
}

func incrementalDeclaredPathBase(dag *ir.DAG, runtimeWorkingDir string) string {
	if dag.WorkingDirExplicit {
		return runtimeWorkingDir
	}
	return dag.WorkingDir
}

type incrementalRedirect struct {
	field    string
	path     string
	artifact bool
}

func validateIncrementalRuntimeRedirectAliases(ctx context.Context, plan *Plan, node *Node) error {
	dag := GetDAGContext(ctx).DAG
	if dag == nil || dag.Type != ir.TypeIncremental {
		return nil
	}
	step := node.Step()
	if !cmnvalue.HasValueReference(step.Stdout) &&
		!cmnvalue.HasValueReference(step.Stderr) &&
		!cmnvalue.HasValueReference(step.StdoutArtifact) &&
		!cmnvalue.HasValueReference(step.StderrArtifact) {
		return nil
	}

	pathKeys := incremental.NewPathKeyResolver()
	declaredPaths := make(map[string]string)
	for _, planNode := range plan.Nodes() {
		step := planNode.Step()
		for _, input := range step.Inputs {
			declaredPaths[pathKeys.ComparisonKey(input.Path)] = fmt.Sprintf("incremental input %s declared by %s", input.Name, step.Name)
		}
		for _, output := range step.Outputs {
			if output.Path != "" {
				declaredPaths[pathKeys.ComparisonKey(output.Path)] = fmt.Sprintf("incremental output produced by %s", step.Name)
			}
		}
	}
	env := GetEnv(ctx)
	return validateIncrementalRedirectAliases(
		ctx,
		step,
		env,
		incrementalDeclaredPathBase(dag, env.WorkingDir),
		declaredPaths,
		pathKeys,
		false,
	)
}

func validateIncrementalRedirectAliases(
	ctx context.Context,
	step ir.Step,
	env Env,
	base string,
	declaredPaths map[string]string,
	pathKeys *incremental.PathKeyResolver,
	deferUnresolved bool,
) error {
	redirects := []incrementalRedirect{
		{field: "stdout", path: step.Stdout},
		{field: "stderr", path: step.Stderr},
	}
	if step.StdoutArtifact != "" {
		redirects = append(redirects, incrementalRedirect{field: "stdout.artifact", path: step.StdoutArtifact, artifact: true})
	}
	if step.StderrArtifact != "" {
		redirects = append(redirects, incrementalRedirect{field: "stderr.artifact", path: step.StderrArtifact, artifact: true})
	}

	resolver := resolverFromEnv(env)
	for _, redirect := range redirects {
		if redirect.path == "" {
			continue
		}
		resolved, err := resolver.String(ctx, redirect.path, cmnvalue.StepArtifactOutputField(redirect.field))
		if err != nil {
			return err
		}
		if cmnvalue.HasValueReference(resolved) {
			if deferUnresolved {
				continue
			}
			return fmt.Errorf("step %s %s path must resolve before execution", step.Name, redirect.field)
		}
		if redirect.artifact {
			rel, err := cleanArtifactOutputPath(resolved)
			if err != nil {
				return fmt.Errorf("step %s %s: %w", step.Name, redirect.field, err)
			}
			artifactDir := ""
			if env.Scope != nil {
				artifactDir, _ = env.Scope.Get(runctx.EnvKeyDAGRunArtifactsDir)
			}
			if strings.TrimSpace(artifactDir) == "" {
				return fmt.Errorf("step %s %s: %s is not set; enable artifacts for this DAG", step.Name, redirect.field, runctx.EnvKeyDAGRunArtifactsDir)
			}
			artifactDir, err = filepath.Abs(artifactDir)
			if err != nil {
				return fmt.Errorf("step %s %s: resolve artifact directory: %w", step.Name, redirect.field, err)
			}
			resolved = filepath.Join(filepath.Clean(artifactDir), filepath.FromSlash(rel))
		} else if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(base, resolved)
		}
		if declaration, ok := declaredPaths[pathKeys.ComparisonKey(filepath.Clean(resolved))]; ok {
			return fmt.Errorf("step %s %s path aliases %s: %s", step.Name, redirect.field, declaration, resolved)
		}
	}
	return nil
}
