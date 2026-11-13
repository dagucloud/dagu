// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// Spec 091, "Flags of the claude provider". Dagu builds the command of an
// agent.run step with the provider claude from the compiled form of the step.
// A key of the step never becomes a free CLI flag, and the dialect of
// configToFlags plays no part: the form --allowedTools <rule>... needs one flag
// with many arguments.

// claudeProvider is the name of the provider whose flags Spec 091 defines.
const claudeProvider = "claude"

// claudePermissionMode is the value of --permission-mode. It is fixed: a tool
// that no allow rule names is refused, and the CLI never asks.
const claudePermissionMode = "dontAsk"

// claudeInvocation is what the command of one attempt needs.
type claudeInvocation struct {
	prompt         string
	permissionMode string
	model          string
	// effort is the value of --effort. It is empty when the step has no key effort.
	effort string
	// appendSystem is the merged system text.
	appendSystem string
	// tools are the names of the built-in tools for --tools.
	tools []string
	// allow and deny are the rules of --allowedTools and --disallowedTools.
	allow []string
	deny  []string
	// mcpConfig is the path of the MCP config that Dagu wrote.
	mcpConfig string
	// addDir is the agent directory, when a skill is mounted.
	addDir     string
	maxTurns   *int
	jsonSchema string
}

// args returns the arguments in the order of the spec. A flag is absent when
// its key is absent; --tools, --strict-mcp-config and --mcp-config are always
// present. Dagu never passes --bare, because the bare mode does not read the
// OAuth credentials of a subscription.
func (c claudeInvocation) args() []string {
	args := []string{
		"-p", c.prompt,
		"--output-format", "stream-json", "--verbose",
		"--no-session-persistence",
		"--permission-mode", c.permissionMode,
	}
	if c.model != "" {
		args = append(args, "--model", c.model)
	}
	if c.effort != "" {
		args = append(args, "--effort", c.effort)
	}
	if c.appendSystem != "" {
		args = append(args, "--append-system-prompt", c.appendSystem)
	}
	args = append(args, "--tools", strings.Join(c.tools, ","))
	if len(c.allow) > 0 {
		args = append(args, "--allowedTools")
		args = append(args, c.allow...)
	}
	if len(c.deny) > 0 {
		args = append(args, "--disallowedTools")
		args = append(args, c.deny...)
	}
	args = append(args, "--strict-mcp-config", "--mcp-config", c.mcpConfig)
	if c.addDir != "" {
		args = append(args, "--add-dir", c.addDir)
	}
	if c.maxTurns != nil {
		args = append(args, "--max-turns", strconv.Itoa(*c.maxTurns))
	}
	if c.jsonSchema != "" {
		args = append(args, "--json-schema", c.jsonSchema)
	}
	return args
}

// claudeMCPServer is one MCP server of the config that Dagu writes, with its
// values already resolved.
type claudeMCPServer struct {
	name    string
	url     string
	headers map[string]string
	allow   []string
	// named marks a MCP server entry of the AI page (Spec 095). It has no URL until
	// the attempt starts its relay. The server then is the relay, with the relay
	// secret, and the credentials of the entry stay in the coordinator.
	named bool
}

// claudeTools is the tool part of the command: the names of the built-in
// tools, and the allow and deny rules.
type claudeTools struct {
	names []string
	allow []string
	deny  []string
}

// deriveClaudeTools maps the neutral tool entries to the values of the CLI
// (Spec 091, "Flags of the claude provider"): read, write and edit give Read,
// Write and Edit; search gives Grep and Glob; shell gives Bash, and each
// pattern gives the rule Bash(<pattern>). A mounted skill adds the tool Skill. A tool of the bridge or of an mcp
// entry gives mcp__<server>__<tool> for each name in allow of its server; the
// bridge comes first in servers.
func deriveClaudeTools(entries []ir.ToolEntry, servers []claudeMCPServer, withSkill bool) claudeTools {
	var tools claudeTools
	addName := func(name string) {
		if slices.Contains(tools.names, name) {
			return
		}
		tools.names = append(tools.names, name)
	}
	addAllow := func(rule string) {
		if slices.Contains(tools.allow, rule) {
			return
		}
		tools.allow = append(tools.allow, rule)
	}
	for _, entry := range entries {
		if entry.Kind != ir.ToolEntryBuiltin || entry.Builtin == nil {
			continue
		}
		builtin := entry.Builtin
		for _, simple := range []struct {
			on    bool
			names []string
		}{
			{builtin.Read, []string{"Read"}},
			{builtin.Write, []string{"Write"}},
			{builtin.Edit, []string{"Edit"}},
			{builtin.Search, []string{"Grep", "Glob"}},
		} {
			if !simple.on {
				continue
			}
			for _, name := range simple.names {
				addName(name)
				addAllow(name)
			}
		}
		if builtin.Shell != nil {
			addName("Bash")
			for _, pattern := range builtin.Shell.Allow {
				addAllow("Bash(" + pattern + ")")
			}
			for _, pattern := range builtin.Shell.Deny {
				tools.deny = append(tools.deny, "Bash("+pattern+")")
			}
		}
	}
	for _, server := range servers {
		for _, tool := range server.allow {
			addAllow("mcp__" + server.name + "__" + tool)
		}
	}
	if withSkill {
		addName("Skill")
		addAllow("Skill")
	}
	return tools
}

// claudeMCPConfig returns the content of the file for --mcp-config. The file
// has the servers of the mcp entries and the server of the tool bridge; with
// none of them it has no server. The type of a server is http (to confirm in
// Spec 091).
func claudeMCPConfig(servers []claudeMCPServer) ([]byte, error) {
	type serverEntry struct {
		Type    string            `json:"type"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers,omitempty"`
	}
	config := struct {
		MCPServers map[string]serverEntry `json:"mcpServers"`
	}{MCPServers: map[string]serverEntry{}}
	for _, server := range servers {
		config.MCPServers[server.name] = serverEntry{Type: "http", URL: server.url, Headers: server.headers}
	}
	return json.Marshal(config)
}

// appendSystemText returns the text of --append-system-prompt: the merged system
// text without its final line ends. It is empty when the system text has only
// white space.
func appendSystemText(system string) string {
	if strings.TrimSpace(system) == "" {
		return ""
	}
	return strings.TrimRight(system, "\n")
}
