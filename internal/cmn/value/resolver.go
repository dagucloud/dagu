// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"fmt"
	"reflect"
	"strconv"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
)

// Resolver resolves workflow values for semantic fields.
type Resolver struct {
	static      StaticScope
	runtime     RuntimeScope
	diagnostics DiagnosticSink
}

// ResolverOption configures a Resolver.
type ResolverOption func(*Resolver)

// NewResolver creates a resolver for the provided static and runtime scopes.
func NewResolver(static StaticScope, runtime RuntimeScope, opts ...ResolverOption) Resolver {
	r := Resolver{static: static, runtime: runtime}
	for _, opt := range opts {
		opt(&r)
	}
	return r
}

// WithDiagnostics reports passive diagnostics to sink.
func WithDiagnostics(sink DiagnosticSink) ResolverOption {
	return func(r *Resolver) {
		r.diagnostics = sink
	}
}

// Validate accepts value-reference syntax because unresolved references are non-fatal.
func (Resolver) Validate(string, Field) error {
	return nil
}

// String resolves raw according to field.
func (r Resolver) String(ctx context.Context, raw string, field Field) (string, error) {
	return r.resolveString(ctx, raw, field)
}

// Int resolves raw according to field and converts the result to an integer.
func (r Resolver) Int(ctx context.Context, raw string, field Field) (int, error) {
	value, err := r.resolveString(ctx, raw, field)
	if err != nil {
		return 0, err
	}
	v, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("failed to convert %q to int: %w", value, err)
	}
	return v, nil
}

// Object resolves every string leaf in obj according to field.
func (r Resolver) Object(ctx context.Context, obj any, field Field) (any, error) {
	policy := policyForField(field)
	ctx = r.withRuntimeEnv(ctx)
	v := reflect.ValueOf(obj)

	transform := func(ctx context.Context, s string) (string, error) {
		if policy.object == objectEvalPipeline {
			return evalStringValue(ctx, s, buildOptions(r.optionsFor(policy)))
		}
		return r.resolveString(ctx, s, field)
	}

	result, err := walkValue(ctx, v, transform)
	if err != nil {
		return obj, err
	}
	return result.Interface(), nil
}

func (r Resolver) resolveString(ctx context.Context, raw string, field Field) (string, error) {
	if raw == "" {
		return "", nil
	}
	policy := policyForField(field)
	ctx = r.withRuntimeEnv(ctx)
	resolved := raw
	var protected map[string]string
	if policy.strict {
		var err error
		resolved, protected, err = resolveBindings(ctx, raw, r.bindingScope(), field.path, r.diagnostics)
		if err != nil {
			if field.path != "" {
				return "", fmt.Errorf("%s: %w", field.path, err)
			}
			return "", err
		}
	}
	evaluated, err := evalString(ctx, resolved, r.optionsFor(policy)...)
	if err != nil {
		return "", err
	}
	return restoreProtectedReferences(evaluated, protected), nil
}

func (r Resolver) bindingScope() RuntimeScope {
	if r.runtime.Consts != nil || r.runtime.Params != nil || r.runtime.Env != nil || len(r.runtime.Steps) > 0 {
		return r.runtime
	}
	return RuntimeScope{Consts: r.static.Consts}
}

func (r Resolver) withRuntimeEnv(ctx context.Context) context.Context {
	if r.runtime.Env == nil {
		return ctx
	}
	return WithEnvScope(ctx, r.runtime.Env)
}

func (r Resolver) optionsFor(policy resolverPolicy) []option {
	opts := make([]option, 0, len(policy.options)+2)
	if r.runtime.Env != nil {
		//nolint:exhaustive // envVariablesNone intentionally contributes no explicit variable map.
		switch policy.envVariables {
		case envVariablesUser:
			if vars := r.runtime.Env.AllUserEnvs(); len(vars) > 0 {
				opts = append(opts, withVariables(vars))
			}
		case envVariablesAll:
			if vars := r.runtime.Env.ToMap(); len(vars) > 0 {
				opts = append(opts, withVariables(vars))
			}
		}
	}
	if len(r.runtime.Steps) > 0 {
		opts = append(opts, withStepMap(r.runtime.Steps))
	}
	opts = append(opts, policy.options...)
	return opts
}

type objectPolicy int

const (
	_ objectPolicy = iota
	objectEvalPipeline
)

type envVariablesPolicy int

const (
	envVariablesNone envVariablesPolicy = iota
	envVariablesUser
	envVariablesAll
)

type resolverPolicy struct {
	strict       bool
	object       objectPolicy
	envVariables envVariablesPolicy
	options      []option
}

func policyForField(field Field) resolverPolicy {
	switch field.kind {
	case fieldWorkflow,
		fieldStepDir,
		fieldDAGWorkingDir,
		fieldAgentWorkingDir,
		fieldContainer,
		fieldSubDAGName,
		fieldSubDAGParams,
		fieldParallelItem,
		fieldParallelItemParam,
		fieldParallelSubDAG:
		return workflowValuePolicy()
	case fieldConstLoad:
		return resolverPolicy{strict: true, options: []option{withoutSubstitute()}}
	case fieldStaticValidation:
		return resolverPolicy{strict: true, options: []option{withoutSubstitute()}}
	case fieldWorkflowObject:
		return workflowValuePolicy()
	case fieldConditionValue:
		return resolverPolicy{strict: true}
	case fieldDAGEnv:
		return resolverPolicy{strict: true, envVariables: envVariablesUser, options: []option{withOSExpansion()}}
	case fieldRuntimeDAGEnv:
		return resolverPolicy{strict: true, envVariables: envVariablesUser, options: []option{withoutSubstitute()}}
	case fieldStepEnv, fieldContainerEnv:
		return resolverPolicy{strict: true}
	case fieldDynamicParamEval:
		return resolverPolicy{strict: true, envVariables: envVariablesUser, options: []option{withOSExpansion(), withShellCommandSubstitution()}}
	case fieldDotenvPath:
		return resolverPolicy{strict: true, options: []option{withOSExpansion(), withoutSubstitute()}}
	case fieldHostConfigObject:
		return resolverPolicy{object: objectEvalPipeline, envVariables: envVariablesUser, options: []option{withoutSubstitute()}}
	case fieldLogPath:
		return resolverPolicy{options: []option{withOSExpansion(), withoutSubstitute()}}
	case fieldServerBasePath, fieldCoordinatorArtifactBaseDir:
		return resolverPolicy{options: []option{withOSExpansion()}}
	case fieldStructuredOutputPath, fieldStructuredOutputLiteral:
		return resolverPolicy{strict: true, options: []option{withoutExpandShell(), withoutSubstitute()}}
	case fieldStepArtifactOutput:
		return resolverPolicy{
			strict: true,
			options: []option{
				withoutSubstitute(),
				withoutDollarEscape(),
				withoutEscapedDollarRecognition(),
			},
		}
	case fieldRetryInteger, fieldRepeatInteger:
		return resolverPolicy{strict: true, options: []option{withOSExpansion()}}
	case fieldDAGShell, fieldStepShell:
		return resolverPolicy{strict: true, options: append([]option{withoutSubstitute()}, commandPolicyOptions(field.command)...)}
	case fieldShellCommand:
		options := append([]option{withoutSubstitute()}, commandPolicyOptions(field.command)...)
		if commandDefersShellVars(field.command) {
			options = append(options, onlyReplaceVars())
		}
		return resolverPolicy{strict: true, options: options}
	case fieldDirectCommand:
		return resolverPolicy{strict: true, options: append(directCommandBaseOptions(field.command), commandPolicyOptions(field.command)...)}
	case fieldConditionCommand:
		return resolverPolicy{strict: true, options: append(conditionCommandBaseOptions(field.command), commandPolicyOptions(field.command)...)}
	case fieldCommandScript:
		options := append([]option{withoutSubstitute()}, commandPolicyOptions(field.command)...)
		if commandDefersShellVars(field.command) {
			options = append(options, onlyReplaceVars())
		}
		return resolverPolicy{strict: true, options: options}
	case fieldTemplateScript:
		return resolverPolicy{options: []option{withNoExpansion()}}
	case fieldExecutorConfig:
		return resolverPolicy{strict: true, envVariables: envVariablesUser, options: []option{withoutSubstitute()}}
	case fieldTemplateConfig:
		return resolverPolicy{strict: true, envVariables: envVariablesUser, options: []option{withoutSubstitute()}}
	}

	return workflowValuePolicy()
}

func workflowValuePolicy() resolverPolicy {
	return resolverPolicy{strict: true, options: []option{withoutSubstitute()}}
}

func directCommandBaseOptions(command CommandContext) []option {
	options := []option{withoutSubstitute()}
	if command.Target == CommandTargetLocal {
		options = append(options, withOSExpansion())
	}
	return options
}

func conditionCommandBaseOptions(command CommandContext) []option {
	options := []option{withoutSubstitute()}
	if command.Target == CommandTargetLocal {
		options = append(options, withOSExpansion())
	}
	return options
}

func commandPolicyOptions(command CommandContext) []option {
	switch command.Target {
	case CommandTargetLocal:
		return localCommandPolicyOptions(command)
	case CommandTargetDocker:
		if command.ShellConfigured {
			return []option{withoutDollarEscape()}
		}
		return nil
	case CommandTargetSSH:
		if command.ShellConfigured {
			return []option{withoutDollarEscape()}
		}
		return []option{withoutExpandShell()}
	}

	return nil
}

func commandDefersShellVars(command CommandContext) bool {
	if command.Target != CommandTargetLocal || !command.ShellConfigured {
		return false
	}
	if len(command.Shell) == 0 {
		return true
	}
	shell := command.Shell[0]
	return cmdutil.IsUnixLikeShell(shell) || cmdutil.IsNixShell(shell)
}

func localCommandPolicyOptions(command CommandContext) []option {
	if !command.ShellConfigured {
		return nil
	}
	shell := command.Shell
	if len(shell) == 0 || shell[0] == "direct" {
		return []option{withOSExpansion()}
	}
	opts := []option{withoutDollarEscape()}
	if cmdutil.IsUnixLikeShell(shell[0]) || cmdutil.IsNixShell(shell[0]) || cmdutil.IsPowerShell(shell[0]) {
		opts = append(opts, withoutExpandEnv())
	}
	return opts
}

// StepOutputReferences returns braced step output references found in raw.
func StepOutputReferences(raw string) []StepOutputReference {
	refs := scanReferences(raw)
	out := make([]StepOutputReference, 0, len(refs))
	for _, ref := range refs {
		if ref.StepOutput == nil {
			continue
		}
		out = append(out, *ref.StepOutput)
	}
	return out
}
