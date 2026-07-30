// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"context"
	"fmt"
	"strings"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/executor"
)

// workerSelectorExtra returns the extra scope entries for selector resolution,
// exposing the parallel item as ITEM when present.
func workerSelectorExtra(runParams executor.RunParams) map[string]string {
	if runParams.ParallelItem == nil {
		return nil
	}
	return map[string]string{"ITEM": *runParams.ParallelItem}
}

// effectiveWorkerSelector returns the selector for the current sub-DAG
// invocation. A step selector overrides the child DAG selector.
func effectiveWorkerSelector(ctx context.Context, step core.Step, childDAG *core.DAG, runParams executor.RunParams) (map[string]string, error) {
	stepSelector, err := resolveWorkerSelector(ctx, step.WorkerSelector, workerSelectorExtra(runParams))
	if err != nil {
		return nil, err
	}
	if len(stepSelector) > 0 {
		return stepSelector, nil
	}
	if childDAG == nil {
		return nil, nil
	}
	if workerSelectorIsDynamic(childDAG.WorkerSelector) {
		return nil, fmt.Errorf("sub-DAG worker_selector must be literal when the step has no worker_selector")
	}
	return childDAG.WorkerSelector, nil
}

func workerSelectorIsDynamic(selector map[string]string) bool {
	for key, value := range selector {
		if strings.Contains(key, "$") || strings.Contains(value, "$") {
			return true
		}
	}
	return false
}

// resolveWorkerSelector evaluates selector keys and values against the runtime
// scope (DAG env, params, step env, step outputs), plus optional extra entries
// such as ITEM for parallel children.
func resolveWorkerSelector(ctx context.Context, selector map[string]string, extra map[string]string) (map[string]string, error) {
	if len(selector) == 0 {
		return selector, nil
	}

	env := runtime.GetEnv(ctx)
	scope := env.Scope
	if scope == nil {
		scope = cmnvalue.NewEnvScope(nil, false)
	}
	if len(extra) > 0 {
		scope = scope.WithEntries(extra, cmnvalue.EnvSourceStepEnv)
	}
	resolver := runtime.ValueResolverWithScope(ctx, scope)

	field := cmnvalue.WorkflowField("worker_selector")
	resolved := make(map[string]string, len(selector))
	for k, v := range selector {
		resolvedKey, err := resolver.String(ctx, k, field)
		if err != nil {
			return nil, fmt.Errorf("failed to eval worker selector key %q: %w", k, err)
		}
		resolvedVal, err := resolver.String(ctx, v, field)
		if err != nil {
			return nil, fmt.Errorf("failed to eval worker selector value %q: %w", v, err)
		}
		key := strings.TrimSpace(resolvedKey)
		if key == "" {
			return nil, fmt.Errorf("worker selector key %q resolved to an empty key", k)
		}
		if _, ok := resolved[key]; ok {
			return nil, fmt.Errorf("worker selector keys resolve to duplicate key %q", key)
		}
		resolved[key] = strings.TrimSpace(resolvedVal)
	}
	return resolved, nil
}
