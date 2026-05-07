// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"slices"

	"github.com/dagucloud/dagu/internal/core/exec"
	workspacepkg "github.com/dagucloud/dagu/internal/workspace"
)

// ToolRegistration contains metadata and a factory for a single tool.
// Each tool registers itself via init() so the registry is the single source of truth.
type ToolRegistration struct {
	// Name is the unique tool identifier (e.g. "bash", "read").
	Name string
	// Label is the human-readable display name for the settings UI.
	Label string
	// Description is a short description for the settings UI.
	Description string
	// DefaultEnabled indicates whether the tool is enabled by default in policy.
	DefaultEnabled bool
	// Factory creates an instance of the tool with the given config.
	Factory func(ToolConfig) *AgentTool
}

// ToolConfig provides runtime configuration for tool construction.
type ToolConfig struct {
	// DAGsDir is the directory containing DAG definition files.
	DAGsDir string
	// DAGStore provides access to DAG definitions for dag_def_manage.
	// Nil means dag_def_manage reports unavailable when called.
	DAGStore exec.DAGStore
	// DAGRunStore provides access to DAG run history for dag_run_manage.
	// Nil means dag_run_manage reports unavailable when called.
	DAGRunStore exec.DAGRunStore
	// DAGRunWatcher provides session-local run watch registrations for dag_run_manage.
	// Nil means dag_run_manage watch actions report unavailable when called.
	DAGRunWatcher DAGRunWatcher
	// DocStore provides access to Markdown docs/runbooks for runbook_manage.
	// Nil means runbook_manage reports unavailable when called.
	DocStore DocStore
	// WorkspaceStore provides workspace names for workspace-scoped tools.
	WorkspaceStore workspacepkg.Store
	// RemoteContextResolver provides access to remote CLI contexts for remote_agent tools.
	// Nil means remote context tools are not available.
	RemoteContextResolver RemoteContextResolver
}

// toolRegistry holds all registered tools. Populated by init() calls.
var toolRegistry []ToolRegistration

// RegisterTool adds a tool to the global registry.
// Must be called from init() functions only — not safe for concurrent use.
// Panics on duplicate name to catch registration bugs early.
func RegisterTool(reg ToolRegistration) {
	for _, existing := range toolRegistry {
		if existing.Name == reg.Name {
			panic("duplicate tool registration: " + reg.Name)
		}
	}
	toolRegistry = append(toolRegistry, reg)
}

// RegisteredTools returns a copy of all registered tool metadata.
func RegisteredTools() []ToolRegistration {
	out := make([]ToolRegistration, len(toolRegistry))
	copy(out, toolRegistry)
	return out
}

// RegisteredToolNames returns sorted names of all registered tools.
func RegisteredToolNames() []string {
	names := make([]string, 0, len(toolRegistry))
	for _, reg := range toolRegistry {
		names = append(names, reg.Name)
	}
	slices.Sort(names)
	return names
}

// IsRegisteredTool returns true if the named tool is in the registry.
func IsRegisteredTool(name string) bool {
	for _, reg := range toolRegistry {
		if reg.Name == name {
			return true
		}
	}
	return false
}
