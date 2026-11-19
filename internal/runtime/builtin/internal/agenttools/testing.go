// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agenttools

// NewRegistryWithSchema returns a registry with one tool that has the given
// input schema, for a test of the tool bridge with a schema that its MCP server
// cannot serve.
func NewRegistryWithSchema(name string, schema map[string]any) *ToolRegistry {
	registry := NewRegistryOfNames(map[string]string{name: "A tool"})
	registry.tools[name].Schema = schema
	return registry
}

// NewRegistryOfNames returns a registry that offers tools with a name and a
// description and no DAG to run. The executor tests of other packages use it to
// offer a catalog without a DAG loader. The map goes from the name to the
// description.
func NewRegistryOfNames(tools map[string]string) *ToolRegistry {
	registry := &ToolRegistry{
		tools:    make(map[string]*toolInfo, len(tools)),
		dagNames: make(map[string]string, len(tools)),
	}
	for name, description := range tools {
		registry.tools[name] = &toolInfo{Name: name, Description: description}
		registry.dagNames[name] = name
	}
	return registry
}
