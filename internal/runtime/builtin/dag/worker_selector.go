// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"context"
	"fmt"
	"strings"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/runtime"
)

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
		resolved[strings.TrimSpace(resolvedKey)] = strings.TrimSpace(resolvedVal)
	}
	return resolved, nil
}
