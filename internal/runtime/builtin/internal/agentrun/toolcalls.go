// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agentrun

import "strings"

// StructuredOutputTool is the tool of a CLI agent that carries the typed
// result. It is not a tool call of the agent.
const StructuredOutputTool = "StructuredOutput"

// bridgeServer is the name of the MCP server of the tool bridge. The tool name
// of a bridge tool has no prefix in the output tool_calls.
const bridgeServer = "dagu"

// NormalizeToolName returns the name of a tool call as the output tool_calls
// lists it.
//
//	mcp__dagu__<name>   -> <name>
//	mcp__<name>__<tool> -> <name>_<tool>
//	Read, Write, Edit   -> read, write, edit
//	Grep, Glob          -> search
//	Bash                -> shell
//	any other name      -> the name that the CLI gave
func NormalizeToolName(name string) string {
	if rest, ok := strings.CutPrefix(name, "mcp__"); ok {
		if server, tool, found := strings.Cut(rest, "__"); found && tool != "" {
			if server == bridgeServer {
				return tool
			}
			return server + "_" + tool
		}
		return name
	}
	switch name {
	case "Read":
		return "read"
	case "Write":
		return "write"
	case "Edit":
		return "edit"
	case "Grep", "Glob":
		return "search"
	case "Bash":
		return "shell"
	default:
		return name
	}
}
