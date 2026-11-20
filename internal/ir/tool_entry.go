// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import "strings"

// ToolEntryKind names the source of a tool entry.
type ToolEntryKind string

const (
	// ToolEntryDAG offers one DAG as a tool.
	ToolEntryDAG ToolEntryKind = "dag"
	// ToolEntryMCP offers tools that an MCP server lists.
	ToolEntryMCP ToolEntryKind = "mcp"
	// ToolEntryBuiltin selects the built-in tools of the CLI. Only an
	// agent.run step has this entry.
	ToolEntryBuiltin ToolEntryKind = "builtin"
)

// ToolEntry is one tool entry of a chat or agent step (Spec 090). The key dags
// holds the DAG entries. The tool map has the categories mcp and builtin. A list
// of entries keeps the order DAG, mcp, then the builtin entry.
type ToolEntry struct {
	Kind ToolEntryKind `json:"kind"`
	// Name is the key name of the entry: the name of the DAG or the label of
	// the MCP server. The tool name comes from it.
	Name string `json:"name,omitempty"`
	// DAG is the DAG that a ToolEntryDAG entry runs.
	DAG string `json:"dag,omitempty"`
	// NameFromDAG is set for an entry of the list form of tools, which is
	// deprecated. The tool name is then the name of the loaded DAG, as on main,
	// and the name of the entry only when the DAG has no name.
	NameFromDAG bool `json:"nameFromDag,omitempty"`
	// Params maps an input name of the tool to a fixed value or to the model.
	Params map[string]ToolParam `json:"params,omitempty"`
	// MCP holds the server of a ToolEntryMCP entry.
	MCP *MCPToolSource `json:"mcp,omitempty"`
	// Builtin holds the choice of a ToolEntryBuiltin entry.
	Builtin *BuiltinTools `json:"builtin,omitempty"`
}

// MaxToolNameLen is the longest tool name that a provider accepts.
const MaxToolNameLen = 64

// ToolNameFor returns the tool name that a key name gives: each character
// outside [A-Za-z0-9_-] becomes an underscore. A DAG entry uses it
// with its name, and an mcp entry uses it for the server tool name.
func ToolNameFor(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			return r
		default:
			return '_'
		}
	}, name)
}

// ToolName returns the tool name of a ToolEntryDAG entry.
func (e ToolEntry) ToolName() string {
	return ToolNameFor(e.Name)
}

// Category returns the category of the entry: dags for a DAG entry, mcp or
// builtin. Only mcp and builtin are keys of the tool map.
func (e ToolEntry) Category() string {
	switch e.Kind {
	case ToolEntryDAG:
		return "dags"
	case ToolEntryMCP:
		return "mcp"
	case ToolEntryBuiltin:
		return "builtin"
	}
	return "builtin"
}

// BuiltinTools is the neutral choice of the built-in tools of an agent.run step.
// Dagu gives it to the CLI as flags.
type BuiltinTools struct {
	Read   bool `json:"read,omitempty"`
	Write  bool `json:"write,omitempty"`
	Edit   bool `json:"edit,omitempty"`
	Search bool `json:"search,omitempty"`
	// Shell is nil when the shell tool is not selected.
	Shell *BuiltinShell `json:"shell,omitempty"`
}

// BuiltinShell holds the command patterns of the shell tool. A pattern is a
// command prefix. A final " *" means any arguments.
type BuiltinShell struct {
	Allow []string `json:"allow"`
	Deny  []string `json:"deny,omitempty"`
}

// ToolParam is the value of one input of a tool entry.
type ToolParam struct {
	// Value is a fixed value. A fixed input is hidden from the model.
	Value any `json:"value,omitempty"`
	// FromAI leaves the input to the model.
	FromAI bool `json:"fromAI,omitempty"`
	// Description replaces the description of the input that the model sees.
	Description string `json:"description,omitempty"`
}

// MCPToolSource is an MCP server that offers tools over HTTP. The connection,
// the listing and each call have a fixed limit of 30 seconds.
type MCPToolSource struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	// Allow lists the tools of the server that join the catalog.
	Allow []string `json:"allow"`
}
