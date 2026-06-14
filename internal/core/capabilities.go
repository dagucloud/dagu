// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"sync"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
)

// ExecutorCapabilities defines what an executor can do.
type ExecutorCapabilities struct {
	// Command indicates whether the executor supports the command field.
	Command bool
	// MultipleCommands indicates whether the executor supports multiple commands.
	MultipleCommands bool
	// Script indicates whether the executor supports the script field.
	Script bool
	// Shell indicates whether the executor uses shell/shellArgs/shellPackages.
	Shell bool
	// Container indicates whether the executor supports step-level container config.
	Container bool
	// SubDAG indicates whether the executor can execute sub-DAGs.
	SubDAG bool
	// WorkerSelector indicates whether the executor supports worker selection.
	WorkerSelector bool
	// LLM indicates whether the executor supports the llm field.
	LLM bool
	// Agent indicates whether the executor supports the agent field.
	Agent bool
	// GetCommandEvalOptions returns eval options for command field evaluation.
	// Step command fields disable backtick substitution by default; this hook
	// lets executors refine the remaining evaluation behavior.
	GetCommandEvalOptions func(ctx context.Context, step Step) []cmnvalue.Option
	// GetScriptEvalOptions returns eval options for script field evaluation.
	// Step script fields disable backtick substitution by default.
	GetScriptEvalOptions func(ctx context.Context, step Step) []cmnvalue.Option
	// GetConfigEvalOptions returns eval options for executor config evaluation.
	// Step config fields disable backtick substitution by default.
	GetConfigEvalOptions func(ctx context.Context, step Step) []cmnvalue.Option
	// GetEvalOptions is the legacy shared hook for command/script evaluation.
	// Deprecated: prefer the field-specific hooks above.
	GetEvalOptions func(ctx context.Context, step Step) []cmnvalue.Option
}

// executorCapabilitiesRegistry is a typed registry of executor capabilities.
type executorCapabilitiesRegistry struct {
	mu   sync.RWMutex
	caps map[string]ExecutorCapabilities
}

var executorCapabilities = executorCapabilitiesRegistry{
	caps: make(map[string]ExecutorCapabilities),
}

// Register registers capabilities for an executor type.
func (r *executorCapabilitiesRegistry) Register(executorType string, caps ExecutorCapabilities) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.caps[executorType] = caps
}

// Unregister removes capabilities for an executor type.
func (r *executorCapabilitiesRegistry) Unregister(executorType string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.caps, executorType)
}

// Get returns capabilities for an executor type.
// Returns an empty ExecutorCapabilities if not registered.
func (r *executorCapabilitiesRegistry) Get(executorType string) ExecutorCapabilities {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if caps, ok := r.caps[executorType]; ok {
		return caps
	}
	// Default: return all false (strict mode)
	return ExecutorCapabilities{}
}

// RegisterExecutorCapabilities registers capabilities for an executor type.
func RegisterExecutorCapabilities(executorType string, caps ExecutorCapabilities) {
	executorCapabilities.Register(executorType, caps)
}

// UnregisterExecutorCapabilities removes capabilities for an executor type.
func UnregisterExecutorCapabilities(executorType string) {
	executorCapabilities.Unregister(executorType)
}

// SupportsCommand returns whether the executor type supports the command field.
func SupportsCommand(executorType string) bool {
	return executorCapabilities.Get(executorType).Command
}

// SupportsMultipleCommands returns whether the executor type supports multiple commands.
func SupportsMultipleCommands(executorType string) bool {
	return executorCapabilities.Get(executorType).MultipleCommands
}

// SupportsScript returns whether the executor type supports the script field.
func SupportsScript(executorType string) bool {
	return executorCapabilities.Get(executorType).Script
}

// SupportsShell returns whether the executor type uses shell configuration.
func SupportsShell(executorType string) bool {
	return executorCapabilities.Get(executorType).Shell
}

// SupportsContainer returns whether the executor type supports step-level container config.
func SupportsContainer(executorType string) bool {
	return executorCapabilities.Get(executorType).Container
}

// SupportsSubDAG returns whether the executor type can execute sub-DAGs.
func SupportsSubDAG(executorType string) bool {
	return executorCapabilities.Get(executorType).SubDAG
}

// SupportsWorkerSelector returns whether the executor type supports worker selection.
func SupportsWorkerSelector(executorType string) bool {
	return executorCapabilities.Get(executorType).WorkerSelector
}

// SupportsLLM returns whether the executor type supports the llm field.
func SupportsLLM(executorType string) bool {
	return executorCapabilities.Get(executorType).LLM
}

// SupportsAgent returns whether the executor type supports the agent field.
func SupportsAgent(executorType string) bool {
	return executorCapabilities.Get(executorType).Agent
}

func appendEvalOptions(base []cmnvalue.Option, extra []cmnvalue.Option) []cmnvalue.Option {
	if len(extra) == 0 {
		return base
	}
	opts := make([]cmnvalue.Option, 0, len(base)+len(extra))
	opts = append(opts, base...)
	opts = append(opts, extra...)
	return opts
}

// CommandEvalOptions returns eval options for the step command field.
// Step command fields disable backtick substitution by default.
func (s Step) CommandEvalOptions(ctx context.Context) []cmnvalue.Option {
	caps := executorCapabilities.Get(s.ExecutorConfig.Type)
	base := []cmnvalue.Option{cmnvalue.WithoutSubstitute()}
	switch {
	case caps.GetCommandEvalOptions != nil:
		return appendEvalOptions(base, caps.GetCommandEvalOptions(ctx, s))
	case caps.GetEvalOptions != nil:
		return appendEvalOptions(base, caps.GetEvalOptions(ctx, s))
	default:
		return base
	}
}

// ScriptEvalOptions returns eval options for the step script field.
// Step script fields disable backtick substitution by default.
func (s Step) ScriptEvalOptions(ctx context.Context) []cmnvalue.Option {
	caps := executorCapabilities.Get(s.ExecutorConfig.Type)
	base := []cmnvalue.Option{cmnvalue.WithoutSubstitute()}
	switch {
	case caps.GetScriptEvalOptions != nil:
		return appendEvalOptions(base, caps.GetScriptEvalOptions(ctx, s))
	case caps.GetCommandEvalOptions != nil:
		return appendEvalOptions(base, caps.GetCommandEvalOptions(ctx, s))
	case caps.GetEvalOptions != nil:
		return appendEvalOptions(base, caps.GetEvalOptions(ctx, s))
	default:
		return base
	}
}

// ConfigEvalOptions returns eval options for the executor config fields.
// Step config fields disable backtick substitution by default.
func (s Step) ConfigEvalOptions(ctx context.Context) []cmnvalue.Option {
	caps := executorCapabilities.Get(s.ExecutorConfig.Type)
	base := []cmnvalue.Option{cmnvalue.WithoutSubstitute()}
	if caps.GetConfigEvalOptions != nil {
		return appendEvalOptions(base, caps.GetConfigEvalOptions(ctx, s))
	}
	return base
}

// EvalOptions returns eval options for this step's executor type command field.
// Deprecated: use CommandEvalOptions, ScriptEvalOptions, or ConfigEvalOptions.
func (s Step) EvalOptions(ctx context.Context) []cmnvalue.Option {
	return s.CommandEvalOptions(ctx)
}
