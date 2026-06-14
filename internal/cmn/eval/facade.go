// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package eval

import (
	"context"

	"github.com/dagucloud/dagu/internal/cmn/value"
)

type Options = value.Options
type Option = value.Option
type StepInfo = value.StepInfo
type EnvSource = value.EnvSource
type EnvEntry = value.EnvEntry
type EnvScope = value.EnvScope

const (
	EnvSourceOS        = value.EnvSourceOS
	EnvSourceDAGEnv    = value.EnvSourceDAGEnv
	EnvSourceDotEnv    = value.EnvSourceDotEnv
	EnvSourceParam     = value.EnvSourceParam
	EnvSourceOutput    = value.EnvSourceOutput
	EnvSourcePresolved = value.EnvSourcePresolved
	EnvSourceSecret    = value.EnvSourceSecret
	EnvSourceStepEnv   = value.EnvSourceStepEnv
	EnvSourceStep      = value.EnvSourceStep
)

func NewOptions() *Options { return value.NewOptions() }

func WithVariables(vars map[string]string) Option { return value.WithVariables(vars) }

func WithStepMap(stepMap map[string]StepInfo) Option { return value.WithStepMap(stepMap) }

func WithoutExpandEnv() Option { return value.WithoutExpandEnv() }

func WithoutExpandShell() Option { return value.WithoutExpandShell() }

func WithoutSubstitute() Option { return value.WithoutSubstitute() }

func WithoutDollarEscape() Option { return value.WithoutDollarEscape() }

func WithOSExpansion() Option { return value.WithOSExpansion() }

func WithNoExpansion() Option { return value.WithNoExpansion() }

func OnlyReplaceVars() Option { return value.OnlyReplaceVars() }

func NewEnvScope(parent *EnvScope, includeOS bool) *EnvScope {
	return value.NewEnvScope(parent, includeOS)
}

func WithEnvScope(ctx context.Context, scope *EnvScope) context.Context {
	return value.WithEnvScope(ctx, scope)
}

func GetEnvScope(ctx context.Context) *EnvScope { return value.GetEnvScope(ctx) }

func ExpandEnvContext(ctx context.Context, s string) string {
	return value.ExpandEnvContext(ctx, s)
}

func ResolveDataPath(ctx context.Context, varName string, raw any, path string) (any, bool) {
	return value.ResolveDataPath(ctx, varName, raw, path)
}

func String(ctx context.Context, input string, opts ...Option) (string, error) {
	return value.String(ctx, input, opts...)
}

func IntString(ctx context.Context, input string, opts ...Option) (int, error) {
	return value.IntString(ctx, input, opts...)
}

func StringFields[T any](ctx context.Context, obj T, opts ...Option) (T, error) {
	return value.StringFields(ctx, obj, opts...)
}

func ExpandReferences(ctx context.Context, input string, dataMap map[string]string) string {
	return value.ExpandReferences(ctx, input, dataMap)
}

func ExpandReferencesWithSteps(ctx context.Context, input string, dataMap map[string]string, stepMap map[string]StepInfo) string {
	return value.ExpandReferencesWithSteps(ctx, input, dataMap, stepMap)
}

func Object[T any](ctx context.Context, obj T, vars map[string]string, opts ...Option) (T, error) {
	return value.Object(ctx, obj, vars, opts...)
}
