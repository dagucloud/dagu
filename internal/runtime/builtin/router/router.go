// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package router

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

var _ executor.Executor = (*routerExecutor)(nil)

type routerExecutor struct {
	stdout io.Writer
	step   ir.Step
}

func newRouter(_ context.Context, step ir.Step) (executor.Executor, error) {
	return &routerExecutor{
		stdout: os.Stdout,
		step:   step,
	}, nil
}

func (e *routerExecutor) SetStdout(out io.Writer) { e.stdout = out }
func (e *routerExecutor) SetStderr(_ io.Writer)   {}
func (*routerExecutor) Kill(_ os.Signal) error    { return nil }

func (e *routerExecutor) Run(ctx context.Context) error {
	if e.step.Router != nil {
		// Resolve the diagnostic with the same value policy used by route preconditions.
		value, err := runtime.ResolveString(ctx, e.step.Router.Value, cmnvalue.ConditionRuntimeValueField("with.value"))
		if err != nil {
			return fmt.Errorf("failed to evaluate router value: %w", err)
		}

		// Mask before writing so every output backend receives safe diagnostics.
		masker := secretMasker(ctx)
		_, _ = fmt.Fprintf(e.stdout, "Router evaluating: %s\n", masker.MaskString(value))
		for _, route := range e.step.Router.Routes {
			line := fmt.Sprintf("  %s -> %v\n", route.Pattern, route.Targets)
			_, _ = fmt.Fprint(e.stdout, masker.MaskString(line))
		}

		if err := checkNumericRoutes(e.step.Router.Routes, value); err != nil {
			return err
		}
	}
	return nil
}

// checkNumericRoutes fails the router when the value cannot be compared against
// a numeric route. Routing is reported here rather than left to each target's
// injected precondition, so an undecidable routing decision produces one
// failure and runs no target at all.
func checkNumericRoutes(routes []ir.RouteEntry, value string) error {
	for _, route := range routes {
		if !stringutil.HasNumericPrefix(route.Pattern) {
			continue
		}
		comparison, err := stringutil.ParseNumericPattern(route.Pattern)
		if err != nil {
			return fmt.Errorf("route %q is an invalid numeric comparison: %w", route.Pattern, err)
		}
		if _, err := comparison.Match(value); err != nil {
			return fmt.Errorf("route %q cannot be evaluated: %w", route.Pattern, err)
		}
	}
	return nil
}

func secretMasker(ctx context.Context) *masking.Masker {
	secrets := runtime.GetDAGContext(ctx).EnvScope.AllSecrets()
	envs := make([]string, 0, len(secrets))
	for name, value := range secrets {
		envs = append(envs, name+"="+value)
	}

	return masking.NewMasker(masking.SourcedEnvVars{Secrets: envs})
}

func init() {
	executor.RegisterExecutor("router", newRouter, nil, registry.ExecutorCapabilities{})
}
