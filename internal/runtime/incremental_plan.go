// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"fmt"
	"strings"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/incremental"
)

func prepareIncrementalPlan(ctx context.Context, plan *Plan) error {
	dag := GetDAGContext(ctx).DAG
	if dag == nil || dag.Type != core.TypeIncremental {
		return nil
	}
	baseEnv, err := NewEnvWithError(ctx, core.Step{})
	if err != nil {
		return err
	}
	base := dag.WorkingDir
	if dag.WorkingDirExplicit {
		base = baseEnv.WorkingDir
	}
	producers := make(map[string]string)

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
			if strings.Contains(resolved, "${") {
				return fmt.Errorf("step %s input %s path must resolve before execution", step.Name, step.Inputs[idx].Name)
			}
			step.Inputs[idx].Path, err = incremental.ResolvePath(resolved, base, false)
			if err != nil {
				return fmt.Errorf("step %s input %s: %w", step.Name, step.Inputs[idx].Name, err)
			}
		}
		for idx := range step.Outputs {
			if step.Outputs[idx].Path == "" {
				continue
			}
			resolved, err := resolver.String(ctx, step.Outputs[idx].Path, cmnvalue.WorkflowField(fmt.Sprintf("steps.%s.outputs.%s.path", step.Name, step.Outputs[idx].Name)))
			if err != nil {
				return err
			}
			if strings.Contains(resolved, "${") {
				return fmt.Errorf("step %s output %s path must resolve before execution", step.Name, step.Outputs[idx].Name)
			}
			step.Outputs[idx].Path, err = incremental.ResolvePath(resolved, base, true)
			if err != nil {
				return fmt.Errorf("step %s output %s: %w", step.Name, step.Outputs[idx].Name, err)
			}
			key := incremental.ComparisonKey(step.Outputs[idx].Path)
			if previous, exists := producers[key]; exists {
				return fmt.Errorf("incremental output path %s has multiple producers: %s and %s", step.Outputs[idx].Path, previous, step.Name)
			}
			producers[key] = step.Name
		}
		for _, input := range step.Inputs {
			for _, output := range step.Outputs {
				if output.Path != "" && incremental.ComparisonKey(input.Path) == incremental.ComparisonKey(output.Path) {
					return fmt.Errorf("step %s declares the same path as input and output: %s", step.Name, input.Path)
				}
			}
		}
		node.SetStep(step)
	}

	for _, node := range plan.Nodes() {
		for _, input := range node.Step().Inputs {
			if producer, ok := producers[incremental.ComparisonKey(input.Path)]; ok {
				if err := plan.AddInferredDependency(producer, node.Name()); err != nil {
					return fmt.Errorf("infer dependency %s -> %s: %w", producer, node.Name(), err)
				}
			}
		}
	}
	return nil
}
