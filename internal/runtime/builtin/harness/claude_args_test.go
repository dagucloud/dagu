// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"slices"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeArgsFollowTheOrderOfTheSpec(t *testing.T) {
	t.Parallel()

	turns := 7
	invocation := claudeInvocation{
		prompt:         "Review it.",
		permissionMode: "dontAsk",
		model:          "sonnet",
		appendSystem:   "Be brief.",
		tools:          []string{"Read", "Grep", "Glob", "Bash"},
		allow:          []string{"Read", "Grep", "Glob", "Bash(git diff *)", "mcp__docs__search"},
		deny:           []string{"Bash(git push *)"},
		mcpConfig:      "/tmp/mcp.json",
		maxTurns:       &turns,
		jsonSchema:     `{"type":"object"}`,
	}
	assert.Equal(t, []string{
		"-p", "Review it.",
		"--output-format", "stream-json", "--verbose",
		"--no-session-persistence",
		"--permission-mode", "dontAsk",
		"--model", "sonnet",
		"--append-system-prompt", "Be brief.",
		"--tools", "Read,Grep,Glob,Bash",
		"--allowedTools", "Read", "Grep", "Glob", "Bash(git diff *)", "mcp__docs__search",
		"--disallowedTools", "Bash(git push *)",
		"--strict-mcp-config", "--mcp-config", "/tmp/mcp.json",
		"--max-turns", "7",
		"--json-schema", `{"type":"object"}`,
	}, invocation.args())
}

// The flag --effort comes right after --model.
func TestClaudeArgsPutEffortAfterModel(t *testing.T) {
	t.Parallel()

	args := claudeInvocation{prompt: "hi", permissionMode: "dontAsk", model: "sonnet", effort: "xhigh", appendSystem: "Be brief.", mcpConfig: "/tmp/m.json"}.args()
	i := slices.Index(args, "--model")
	assert.Equal(t, []string{"--model", "sonnet", "--effort", "xhigh", "--append-system-prompt"}, args[i:i+5])

	args = claudeInvocation{prompt: "hi", permissionMode: "dontAsk", effort: "low", mcpConfig: "/tmp/m.json"}.args()
	assert.Equal(t, "low", args[slices.Index(args, "--effort")+1], "an effort with no model still passes the flag")
	assert.NotContains(t, args, "--model")
}

// A flag is absent when its key is absent. The flags --tools, --strict-mcp-config
// and --mcp-config are always there, and --bare never is.
func TestClaudeArgsMinimal(t *testing.T) {
	t.Parallel()

	args := claudeInvocation{prompt: "hi", permissionMode: "plan", mcpConfig: "/tmp/m.json"}.args()
	assert.Equal(t, []string{
		"-p", "hi",
		"--output-format", "stream-json", "--verbose",
		"--no-session-persistence",
		"--permission-mode", "plan",
		"--tools", "",
		"--strict-mcp-config", "--mcp-config", "/tmp/m.json",
	}, args)
	assert.NotContains(t, args, "--bare")
}

func TestDeriveClaudeTools(t *testing.T) {
	t.Parallel()

	entries := []ir.ToolEntry{
		{Kind: ir.ToolEntryBuiltin, Builtin: &ir.BuiltinTools{
			Read: true, Search: true,
			Shell: &ir.BuiltinShell{Allow: []string{"git diff *", "go test"}, Deny: []string{"git push *"}},
		}},
		{Kind: ir.ToolEntryBuiltin, Builtin: &ir.BuiltinTools{Write: true, Edit: true}},
	}
	servers := []claudeMCPServer{{name: "docs", allow: []string{"search", "fetch"}}}

	tools := deriveClaudeTools(entries, servers, false)
	assert.Equal(t, []string{"Read", "Grep", "Glob", "Bash", "Write", "Edit"}, tools.names)
	assert.Equal(t, []string{
		"Read", "Grep", "Glob", "Bash(git diff *)", "Bash(go test)", "Write", "Edit",
		"mcp__docs__search", "mcp__docs__fetch",
	}, tools.allow)
	assert.Equal(t, []string{"Bash(git push *)"}, tools.deny)

	none := deriveClaudeTools(nil, nil, false)
	assert.Empty(t, none.names)
	assert.Empty(t, none.allow)
	assert.Empty(t, none.deny)
}

// The tools of the bridge are mcp__dagu__<name>, and they come before the tools
// of an mcp entry.
func TestDeriveClaudeToolsWithBridge(t *testing.T) {
	t.Parallel()

	entries := []ir.ToolEntry{{Kind: ir.ToolEntryBuiltin, Builtin: &ir.BuiltinTools{Read: true}}}
	servers := []claudeMCPServer{
		{name: "dagu", allow: []string{"refund", "lookup"}},
		{name: "docs", allow: []string{"search"}},
	}

	tools := deriveClaudeTools(entries, servers, false)
	assert.Equal(t, []string{"Read"}, tools.names, "the bridge tools are not built-in tools")
	assert.Equal(t, []string{
		"Read", "mcp__dagu__refund", "mcp__dagu__lookup", "mcp__docs__search",
	}, tools.allow)
}

func TestClaudeMCPConfig(t *testing.T) {
	t.Parallel()

	data, err := claudeMCPConfig(nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{"mcpServers":{}}`, string(data))

	data, err = claudeMCPConfig([]claudeMCPServer{{name: "docs", url: "https://x/mcp", headers: map[string]string{"Authorization": "Bearer t"}}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"mcpServers":{"docs":{"type":"http","url":"https://x/mcp","headers":{"Authorization":"Bearer t"}}}}`, string(data))
}

// A mounted skill adds the tool Skill to --tools and to the allow rules.
func TestDeriveClaudeToolsWithSkill(t *testing.T) {
	t.Parallel()

	tools := deriveClaudeTools([]ir.ToolEntry{{Kind: ir.ToolEntryBuiltin, Builtin: &ir.BuiltinTools{Read: true}}}, nil, true)
	assert.Equal(t, []string{"Read", "Skill"}, tools.names)
	assert.Equal(t, []string{"Read", "Skill"}, tools.allow)

	skillOnly := deriveClaudeTools(nil, nil, true)
	assert.Equal(t, []string{"Skill"}, skillOnly.names)
	assert.Equal(t, []string{"Skill"}, skillOnly.allow)
}

func TestAppendSystemText(t *testing.T) {
	t.Parallel()

	assert.Empty(t, appendSystemText(""))
	assert.Empty(t, appendSystemText(" \n"))
	assert.Equal(t, "Be brief.", appendSystemText("Be brief.\n"))
	assert.Equal(t, "Be brief.\n\nBe kind.", appendSystemText("Be brief.\n\nBe kind."))
}
