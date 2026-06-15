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
	// CommandContext returns command execution facts for command field resolution.
	CommandContext func(ctx context.Context, step Step) cmnvalue.CommandContext
	// ScriptContext returns command execution facts for script field resolution.
	ScriptContext func(ctx context.Context, step Step) cmnvalue.CommandContext
	// ConfigProfile returns the executor configuration resolution profile.
	ConfigProfile func(ctx context.Context, step Step) cmnvalue.ConfigProfile
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

// CommandResolution returns command execution facts for command field resolution.
func (s Step) CommandResolution(ctx context.Context) cmnvalue.CommandContext {
	caps := executorCapabilities.Get(s.ExecutorConfig.Type)
	if caps.CommandContext != nil {
		return caps.CommandContext(ctx, s)
	}
	return cmnvalue.CommandContext{}
}

// ScriptResolution returns command execution facts for script field resolution.
func (s Step) ScriptResolution(ctx context.Context) cmnvalue.CommandContext {
	caps := executorCapabilities.Get(s.ExecutorConfig.Type)
	if caps.ScriptContext != nil {
		return caps.ScriptContext(ctx, s)
	}
	if caps.CommandContext != nil {
		return caps.CommandContext(ctx, s)
	}
	return cmnvalue.CommandContext{}
}

// ConfigResolutionProfile returns the semantic profile for executor config resolution.
func (s Step) ConfigResolutionProfile(ctx context.Context) cmnvalue.ConfigProfile {
	caps := executorCapabilities.Get(s.ExecutorConfig.Type)
	if caps.ConfigProfile != nil {
		return caps.ConfigProfile(ctx, s)
	}
	return cmnvalue.ConfigProfileDefault
}
