// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const toolChatHead = `
steps:
  - id: ask
    action: chat.completion
    with:
      provider: local
      model: m
      base_url: http://localhost:1
      prompt: hi
`

func loadToolDAG(t *testing.T, tools string, opts ...spec.LoadOption) (*ir.LLMConfig, error) {
	t.Helper()
	dag, err := spec.LoadYAML(t.Context(), []byte(toolChatHead+tools), opts...)
	if err != nil {
		return nil, err
	}
	require.Len(t, dag.Steps, 1)
	require.NotNil(t, dag.Steps[0].LLM)
	return dag.Steps[0].LLM, nil
}

// The entries of dags come before the entries of mcp, in whatever order the
// author writes the keys.
func TestToolEntriesKeepTheCategoryOrder(t *testing.T) {
	t.Parallel()

	cfg, err := loadToolDAG(t, `
      tools:
        mcp:
          - name: docs
            url: https://mcp.example.com/mcp
            allow: [search]
      dags:
        - name: lookup
        - name: refund
`)
	require.NoError(t, err)
	require.Len(t, cfg.ToolEntries, 3)
	assert.Equal(t, ir.ToolEntryDAG, cfg.ToolEntries[0].Kind)
	assert.Equal(t, "lookup", cfg.ToolEntries[0].DAG)
	assert.Equal(t, "lookup", cfg.ToolEntries[0].Name)
	assert.Equal(t, "refund", cfg.ToolEntries[1].DAG)
	assert.Equal(t, ir.ToolEntryMCP, cfg.ToolEntries[2].Kind)
	assert.Equal(t, "docs", cfg.ToolEntries[2].Name)
	assert.True(t, cfg.HasTools())
}

// The list form of main, a list of DAG names, is deprecated. It equals the
// entries of the key dags with no params.
func TestToolListFormIsTheDAGEntriesWithNoParams(t *testing.T) {
	t.Parallel()

	cfg, err := loadToolDAG(t, `
      tools:
        - lookup
        - refund
`)
	require.NoError(t, err)
	require.Len(t, cfg.ToolEntries, 2)
	for i, name := range []string{"lookup", "refund"} {
		entry := cfg.ToolEntries[i]
		assert.Equal(t, ir.ToolEntryDAG, entry.Kind)
		assert.Equal(t, name, entry.Name)
		assert.Equal(t, name, entry.DAG)
		assert.True(t, entry.NameFromDAG)
		assert.Empty(t, entry.Params)
	}
	assert.True(t, cfg.HasTools())
}

// The list form is valid in the llm block too, as on main.
func TestToolListFormInTheLLMBlock(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(t.Context(), []byte(`
llm:
  provider: local
  model: m
  base_url: http://localhost:1
  tools: [lookup]
steps:
  - id: ask
    action: chat.completion
    with:
      prompt: hi
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)
	require.NotNil(t, dag.Steps[0].LLM)
	require.Len(t, dag.Steps[0].LLM.ToolEntries, 1)
	assert.Equal(t, "lookup", dag.Steps[0].LLM.ToolEntries[0].DAG)
}

// The llm block of a DAG takes the key dags as the with of a step does. The key
// tools of the block has no category dags.
func TestToolDAGsInTheLLMBlock(t *testing.T) {
	t.Parallel()

	const steps = `
steps:
  - id: ask
    action: chat.completion
    with:
      prompt: hi
`
	dag, err := spec.LoadYAML(t.Context(), []byte(`
llm:
  provider: local
  model: m
  base_url: http://localhost:1
  dags:
    - name: lookup
  tools:
    mcp:
      - { name: docs, url: "https://mcp.example.com/mcp", allow: [search] }
`+steps))
	require.NoError(t, err)
	require.NotNil(t, dag.LLM)
	require.Len(t, dag.LLM.ToolEntries, 2)
	assert.Equal(t, ir.ToolEntryDAG, dag.LLM.ToolEntries[0].Kind)
	assert.Equal(t, ir.ToolEntryMCP, dag.LLM.ToolEntries[1].Kind)

	_, err = spec.LoadYAML(t.Context(), []byte(`
llm:
  provider: local
  model: m
  base_url: http://localhost:1
  tools:
    dags:
      - name: lookup
`+steps))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `llm.tools.dags`)
	assert.Contains(t, err.Error(), `unknown key "dags" in the tool map; valid keys are mcp`)
}

func TestToolEntryDAGKeys(t *testing.T) {
	t.Parallel()

	cfg, err := loadToolDAG(t, `
      dags:
        - name: refund
          params:
            customer_id: ${CUSTOMER}
            amount:
              from_ai:
                description: Amount in euros
            note:
              from_ai: true
`)
	require.NoError(t, err)
	entry := cfg.ToolEntries[0]
	assert.Equal(t, "refund", entry.Name)
	assert.Equal(t, ir.ToolParam{Value: "${CUSTOMER}"}, entry.Params["customer_id"])
	assert.Equal(t, ir.ToolParam{FromAI: true, Description: "Amount in euros"}, entry.Params["amount"])
	assert.Equal(t, ir.ToolParam{FromAI: true}, entry.Params["note"])
}

func TestToolEntryMCP(t *testing.T) {
	t.Parallel()

	cfg, err := loadToolDAG(t, `
      tools:
        mcp:
          - name: docs
            url: https://mcp.example.com/mcp
            headers:
              Authorization: Bearer abc
            allow: [search, read]
`)
	require.NoError(t, err)
	entry := cfg.ToolEntries[0]
	require.NotNil(t, entry.MCP)
	assert.Equal(t, "docs", entry.Name)
	assert.Equal(t, "https://mcp.example.com/mcp", entry.MCP.URL)
	assert.Equal(t, map[string]string{"Authorization": "Bearer abc"}, entry.MCP.Headers)
	assert.Equal(t, []string{"search", "read"}, entry.MCP.Allow)
}

func TestToolEntryMCPAcceptsAValueReferenceURL(t *testing.T) {
	t.Parallel()

	_, err := loadToolDAG(t, `
      tools:
        mcp:
          - name: docs
            url: ${MCP_URL}
            allow: [search]
`)
	require.NoError(t, err)
}

func TestToolLimitsReachTheStep(t *testing.T) {
	t.Parallel()

	cfg, err := loadToolDAG(t, `
      dags: [{name: lookup}]
      max_tools: 3
      tool_result_max_bytes: 0
`)
	require.NoError(t, err)
	assert.Equal(t, 3, cfg.GetMaxTools())
	assert.Equal(t, 0, cfg.GetToolResultMaxBytes())

	cfg, err = loadToolDAG(t, `
      dags: [{name: lookup}]
`)
	require.NoError(t, err)
	assert.Equal(t, ir.DefaultMaxTools, cfg.GetMaxTools())
	assert.Equal(t, ir.DefaultToolResultMaxBytes, cfg.GetToolResultMaxBytes())
}

func TestToolMapErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tools   string
		message string
	}{
		{"list of objects in place of names", "tools:\n        - {name: lookup}\n", "must be the name of a DAG"},
		{"empty name in the list form", "tools:\n        - \"\"\n", "must be the name of a DAG"},
		{"string in place of the map", "tools: lookup\n", "tools must be a map"},
		{"actions category", "tools:\n        actions:\n          - name: ops.notify\n", `unknown key "actions" in the tool map; valid keys are mcp`},
		{"unknown category", "tools:\n        toolsets:\n          - name: support\n", `unknown key "toolsets" in the tool map; valid keys are mcp`},
		{"dags in the tool map", "tools:\n        dags:\n          - name: lookup\n", `llm.tools.dags`},
		{"dags in the tool map message", "tools:\n        dags:\n          - name: lookup\n", `unknown key "dags" in the tool map; valid keys are mcp`},
		{"dags with the list form", "tools: [lookup]\n      dags:\n        - name: other\n", `key "dags" and key "tools" contradict each other`},
		{"dags not a list", "dags: lookup\n", "dags must be a list of entries"},
		{"dags a map", "dags:\n        name: lookup\n", "dags must be a list of entries, got a map"},
		{"plain string entry", "dags:\n        - lookup\n", "an entry of dags must be an object"},
		{"no name", "dags:\n        - params: {a: 1}\n", "name is required"},
		{"empty name", "dags:\n        - name: \"\"\n", "name is required"},
		{"unknown key", "dags:\n        - name: a\n          bogus: 1\n", `unknown key "bogus" in an entry of dags; valid keys are name, params`},
		{"if on a dag entry", "dags:\n        - name: a\n          if: [\"true\"]\n", `unknown key "if" in an entry of dags`},
		{"if on an mcp entry", "tools:\n        mcp:\n          - name: docs\n            url: http://x\n            allow: [t]\n            if: [\"true\"]\n", `unknown key "if" in an entry of mcp; valid keys are name, url, headers, allow`},
		{"description on a dag entry", "dags:\n        - name: a\n          description: d\n", `unknown key "description" in an entry of dags`},
		{"params on mcp", "tools:\n        mcp:\n          - name: docs\n            url: http://x\n            allow: [t]\n            params: {a: 1}\n", `unknown key "params" in an entry of mcp`},
		{"from_ai false", "dags:\n        - name: a\n          params:\n            x:\n              from_ai: false\n", "from_ai must be true"},
		{"from_ai unknown key", "dags:\n        - name: a\n          params:\n            x:\n              from_ai: {title: t}\n", `unknown key "title" in from_ai; valid keys are description`},
		{"from_ai with another key", "dags:\n        - name: a\n          params:\n            x:\n              from_ai: true\n              default: 3\n", `unknown key "default" in an input with from_ai; valid keys are from_ai`},
		{"mcp without name", "tools:\n        mcp:\n          - url: http://x\n            allow: [t]\n", "name is required"},
		{"mcp bad name", "tools:\n        mcp:\n          - name: Docs\n            url: http://x\n            allow: [t]\n", "must match"},
		{"mcp with an empty url", "tools:\n        mcp:\n          - name: docs\n            url: \"\"\n            allow: [t]\n", "url must be a non-empty string"},
		{"mcp with a url that is not text", "tools:\n        mcp:\n          - name: docs\n            url: 5\n            allow: [t]\n", "url must be a non-empty string"},
		{"named mcp with headers", "tools:\n        mcp:\n          - name: docs\n            headers: {Authorization: x}\n            allow: [t]\n", "headers is not valid on an mcp entry with no url"},
		{"named mcp without allow", "tools:\n        mcp:\n          - name: docs\n", "allow is required"},
		{"named mcp with a long name", "tools:\n        mcp:\n          - name: a" + strings.Repeat("b", 64) + "\n            allow: [t]\n", "name of an mcp entry with no url must match ^[a-z][a-z0-9-]{0,63}$"},
		{"named mcp named dagu", "tools:\n        mcp:\n          - name: dagu\n            allow: [t]\n", "name of the tool bridge"},
		{"mcp without allow", "tools:\n        mcp:\n          - name: docs\n            url: http://x\n", "allow is required"},
		{"mcp empty allow", "tools:\n        mcp:\n          - name: docs\n            url: http://x\n            allow: []\n", "allow is required"},
		{"mcp bad scheme", "tools:\n        mcp:\n          - name: docs\n            url: ftp://x\n            allow: [t]\n", "http or https"},
		{"mcp timeout", "tools:\n        mcp:\n          - name: docs\n            url: http://x\n            allow: [t]\n            timeout: 5s\n", `unknown key "timeout" in an entry of mcp`},
		{"mcp named dagu", "tools:\n        mcp:\n          - name: dagu\n            url: http://x\n            allow: [t]\n", "name of the tool bridge"},
		{"mcp duplicate name", "tools:\n        mcp:\n          - name: docs\n            url: http://x\n            allow: [t]\n          - name: docs\n            url: http://y\n            allow: [t]\n", `mcp name "docs" is also the name of`},
		{"duplicate dags key", "dags: [{name: a}]\n      dags: [{name: b}]\n", `mapping key "dags" already defined`},
		{"duplicate entry key", "dags:\n        - name: a\n          name: b\n", `mapping key "name" already defined`},
		{"builtin on chat", "tools:\n        builtin:\n          read: true\n", "the category builtin is valid only on an agent.run step"},
		{"bad max_tools", "dags: [{name: a}]\n      max_tools: 0\n", "max_tools must be at least 1"},
		{"bad result limit", "dags: [{name: a}]\n      tool_result_max_bytes: -1\n", "tool_result_max_bytes must not be negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := loadToolDAG(t, "      "+tt.tools)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.message)
		})
	}
}

// Two entries that give the same tool name are a validation error that names
// both paths. A tool name is the name of the entry with each character outside
// [A-Za-z0-9_-] replaced by an underscore.
func TestToolNameRules(t *testing.T) {
	t.Parallel()

	t.Run("names that give one tool name", func(t *testing.T) {
		t.Parallel()
		_, err := loadToolDAG(t, "      dags:\n        - name: ops_notify\n        - name: ops.notify\n")
		require.Error(t, err)
		assert.Contains(t, err.Error(), `llm.dags[1]`)
		assert.Contains(t, err.Error(), `tool name "ops_notify" is also the tool name of llm.dags[0]`)
	})
	t.Run("same name twice", func(t *testing.T) {
		t.Parallel()
		_, err := loadToolDAG(t, "      dags:\n        - name: a\n        - name: a\n")
		require.Error(t, err)
		assert.Contains(t, err.Error(), `tool name "a" is also the tool name of llm.dags[0]`)
	})
	t.Run("name above 64 characters", func(t *testing.T) {
		t.Parallel()
		_, err := loadToolDAG(t, "      dags:\n        - name: "+strings.Repeat("a", 65)+"\n")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "above the limit of 64")
	})
	t.Run("name of 64 characters", func(t *testing.T) {
		t.Parallel()
		_, err := loadToolDAG(t, "      dags:\n        - name: "+strings.Repeat("a", 64)+"\n")
		require.NoError(t, err)
	})
}

func TestToolEntryToolNameSanitizesTheName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "ops_notify", ir.ToolEntry{Name: "ops.notify"}.ToolName())
	assert.Equal(t, "a_b_c", ir.ToolEntry{Name: "a b.c"}.ToolName())
	assert.Equal(t, "keep-this_1", ir.ToolEntry{Name: "keep-this_1"}.ToolName())
}

// A folder named toolsets is an ordinary folder: Dagu does not read it for
// tools.
func TestToolsetsFolderIsNotRead(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "toolsets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "toolsets", "support.yaml"), []byte("description: d\ntools: [a]\n"), 0o600))
	_, err := loadToolDAG(t, "      tools:\n        toolsets:\n          - name: support\n", spec.WithDAGsDir(dir))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown key "toolsets" in the tool map`)
}

// An mcp entry with no url names a MCP server entry of the AI page (Spec 095).
// The loader keeps the name and the key allow, and no URL. The inline form with
// url does not change.
func TestToolEntryMCPNamedForm(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(t.Context(), []byte(toolChatHead+`
      tools:
        mcp:
          - name: docs
            allow: [search, read]
          - name: inline
            url: https://mcp.example.com/mcp
            allow: [x]
`))
	require.NoError(t, err)
	step := dag.Steps[0]
	entries := step.LLM.ToolEntries
	require.Len(t, entries, 2)
	assert.True(t, entries[0].IsNamedMCP())
	assert.Empty(t, entries[0].MCP.URL)
	assert.Equal(t, []string{"search", "read"}, entries[0].MCP.Allow)
	assert.False(t, entries[1].IsNamedMCP())

	// Only the named entry is a target of the relay.
	assert.Equal(t, []string{"docs"}, step.AIMCPServers())
	assert.Equal(t, []string{"search", "read"}, step.MCPAllow("docs"))
	assert.Nil(t, step.MCPAllow("inline"))
	assert.Equal(t, []string{ir.CapabilityAIRelay}, dag.RequiredCapabilities())
	message, ok := dag.DescribeMissingAIRelay()
	require.True(t, ok)
	assert.Contains(t, message, `the step "ask" names the MCP server "docs"`)

	inline, err := spec.LoadYAML(t.Context(), []byte(toolChatHead+`
      tools:
        mcp:
          - { name: docs, url: "https://mcp.example.com/mcp", allow: [search] }
`))
	require.NoError(t, err)
	assert.Nil(t, inline.Steps[0].AIMCPServers())
	assert.Nil(t, inline.RequiredCapabilities(), "the inline form needs no worker capability")
}

// The llm block of a DAG cannot name a MCP server entry: no relay serves it.
func TestToolEntryMCPNamedFormIsRefusedInTheLLMBlock(t *testing.T) {
	t.Parallel()

	for name, text := range map[string]string{
		"dag llm": `
llm:
  provider: local
  model: m
  base_url: http://localhost:1
  tools:
    mcp:
      - { name: docs, allow: [search] }
steps:
  - run: echo hi
`,
	} {
		_, err := spec.LoadYAML(t.Context(), []byte(text))
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "an mcp entry with no url names a MCP server entry of the AI page", name)
	}

	// The same llm block with an inline entry stays valid.
	_, err := spec.LoadYAML(t.Context(), []byte(`
llm:
  provider: local
  model: m
  base_url: http://localhost:1
  tools:
    mcp:
      - { name: docs, url: "https://mcp.example.com/mcp", allow: [search] }
steps:
  - run: echo hi
`))
	require.NoError(t, err)
}

// The step resolves the url and the headers of an inline mcp entry and the fixed
// inputs of a dags entry when it starts. A reference to the output of another
// step in them orders the steps, on chat.completion and on agent.run.
func TestToolEntryReferencesInferDependencies(t *testing.T) {
	t.Parallel()

	const discover = `
steps:
  - id: discover
    run: printf 'URL=http://127.0.0.1:1\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: URL
`
	for name, tc := range map[string]struct{ step, field string }{
		"chat mcp url": {`
  - id: ask
    action: chat.completion
    with:
      provider: local
      model: m
      prompt: hi
      tools:
        mcp:
          - { name: docs, url: "${steps.discover.outputs.URL}", allow: [search] }
`, "steps[1].llm.tools.mcp[0].url"},
		"chat mcp header": {`
  - id: ask
    action: chat.completion
    with:
      provider: local
      model: m
      prompt: hi
      tools:
        mcp:
          - { name: docs, url: "http://127.0.0.1:2", headers: { X-Base: "${steps.discover.outputs.URL}" }, allow: [search] }
`, "steps[1].llm.tools.mcp[0].headers.X-Base"},
		"chat dags input": {`
  - id: ask
    action: chat.completion
    with:
      provider: local
      model: m
      prompt: hi
      dags:
        - { name: lookup, params: { BASE: "${steps.discover.outputs.URL}" } }
`, "steps[1].llm.dags[0].params.BASE"},
		"agent dags input": {`
  - id: ask
    action: agent.run
    with:
      provider: claude
      endpoint: { name: test-endpoint, model: claude-test }
      prompt: hi
      dags:
        - { name: lookup, params: { BASE: "${steps.discover.outputs.URL}" } }
`, "steps[1].with.dags[0].params.BASE"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dag, err := spec.LoadYAML(t.Context(), []byte(discover+tc.step), spec.WithDAGsDir(t.TempDir()))
			require.NoError(t, err)
			ask := dag.Steps[1]
			require.Len(t, ask.InferredDepends, 1)
			assert.Equal(t, "discover", ask.InferredDepends[0].Step)
			assert.Equal(t, tc.field, ask.InferredDepends[0].Field)
		})
	}
}
