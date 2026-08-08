// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package registry

import (
	"context"
	"sync"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
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
	// CommandContext returns command execution facts for command field resolution.
	CommandContext func(ctx context.Context, step ir.Step) cmnvalue.CommandContext
	// ScriptContext returns command execution facts for script field resolution.
	ScriptContext func(ctx context.Context, step ir.Step) cmnvalue.CommandContext
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

// Contains reports whether capabilities were registered for an executor type.
func (r *executorCapabilitiesRegistry) Contains(executorType string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.caps[executorType]
	return ok
}

// RegisterExecutorCapabilities registers capabilities for an executor type.
func RegisterExecutorCapabilities(executorType string, caps ExecutorCapabilities) {
	executorCapabilities.Register(executorType, caps)
}

// UnregisterExecutorCapabilities removes capabilities for an executor type.
func UnregisterExecutorCapabilities(executorType string) {
	executorCapabilities.Unregister(executorType)
}

// ExecutorCapabilitiesFor returns the registered capabilities for an executor type.
func ExecutorCapabilitiesFor(executorType string) ExecutorCapabilities {
	return executorCapabilities.Get(executorType)
}

// IsExecutorRegistered reports whether an executor type has registered metadata.
func IsExecutorRegistered(executorType string) bool {
	return executorCapabilities.Contains(executorType)
}

// CommandResolution returns command execution facts for command field resolution.
func CommandResolution(ctx context.Context, s ir.Step) cmnvalue.CommandContext {
	caps := executorCapabilities.Get(s.ExecutorConfig.Type)
	if caps.CommandContext != nil {
		return caps.CommandContext(ctx, s)
	}
	return cmnvalue.CommandContext{}
}

// ScriptResolution returns command execution facts for script field resolution.
func ScriptResolution(ctx context.Context, s ir.Step) cmnvalue.CommandContext {
	caps := executorCapabilities.Get(s.ExecutorConfig.Type)
	if caps.ScriptContext != nil {
		return caps.ScriptContext(ctx, s)
	}
	if caps.CommandContext != nil {
		return caps.CommandContext(ctx, s)
	}
	return cmnvalue.CommandContext{}
}
