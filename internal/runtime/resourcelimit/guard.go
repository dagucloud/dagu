// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package resourcelimit

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/internal/core"
)

// Options describes resource limits for one DAG run.
type Options struct {
	DAGName  string
	DAGRunID string
	Limits   *core.ResourceLimits
}

// Result describes whether the requested limits are enforced.
type Result struct {
	Enforced bool
	Enforcer string
	Warning  string
}

type nativeGuard interface {
	AssignProcess(pid int) error
	Close(ctx context.Context) error
	Enforcer() string
}

// Guard owns an OS-specific resource limit scope for a DAG run.
type Guard struct {
	native nativeGuard
	result Result
}

type contextKey struct{}

var startNative = startNativeGuard

// Start attempts to prepare OS-native resource enforcement. It never returns an
// error because resource enforcement is a best-effort runtime capability.
func Start(ctx context.Context, opts Options) *Guard {
	if opts.Limits == nil || (opts.Limits.CPUMillis == 0 && opts.Limits.MemoryBytes == 0) {
		return &Guard{}
	}

	native, err := startNative(ctx, opts)
	if err != nil {
		return &Guard{
			result: Result{
				Enforcer: "none",
				Warning:  fmt.Sprintf("resource limits requested but not enforced: %v", err),
			},
		}
	}

	return &Guard{
		native: native,
		result: Result{
			Enforced: true,
			Enforcer: native.Enforcer(),
		},
	}
}

// Result returns enforcement status for the guard.
func (g *Guard) Result() Result {
	if g == nil {
		return Result{}
	}
	return g.result
}

// AssignProcess adds a process to the resource-limited scope.
func (g *Guard) AssignProcess(pid int) error {
	if g == nil || g.native == nil || pid <= 0 {
		return nil
	}
	return g.native.AssignProcess(pid)
}

// Close releases guard resources.
func (g *Guard) Close(ctx context.Context) error {
	if g == nil || g.native == nil {
		return nil
	}
	return g.native.Close(ctx)
}

// WithGuard stores a resource guard in context.
func WithGuard(ctx context.Context, guard *Guard) context.Context {
	if guard == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, guard)
}

// FromContext returns the resource guard stored in context, if present.
func FromContext(ctx context.Context) *Guard {
	if guard, ok := ctx.Value(contextKey{}).(*Guard); ok {
		return guard
	}
	return nil
}
