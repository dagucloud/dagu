// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const reviewerSchema = `
    output_schema:
      type: object
      additionalProperties: false
      properties:
        verdict: { type: string, enum: [accept, reject] }
      required: [verdict]
`

func loadAgentRun(t *testing.T, dir, yamlText string) (*ir.DAG, error) {
	t.Helper()
	return spec.LoadYAML(t.Context(), []byte(yamlText), spec.WithDAGsDir(dir))
}

// loadAgentRunStep loads a DAG with one agent.run step whose with is the text.
func loadAgentRunStep(t *testing.T, dir, with string) (*ir.DAG, error) {
	t.Helper()
	return loadAgentRun(t, dir, "steps:\n  - id: s\n    action: agent.run\n    with:\n      "+with+"\n")
}

// An agent.run step compiles to harness.run and keeps its compiled form.
func TestAgentRunCompilesToHarness(t *testing.T) {
	t.Parallel()

	dag, err := loadAgentRun(t, t.TempDir(), `
params: [PATCH_ID]
steps:
  - id: review
    action: agent.run
    timeout_sec: 120`+reviewerSchema+`    with:
      provider: claude
      endpoint:
        name: test-endpoint
        model: sonnet
      prompt: "Review the patch ${PATCH_ID}."
      system: Report only real defects.
      max_tool_iterations: 20
      max_tool_calls: 7
      tools:
        builtin: { read: true, search: true }
`)
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)
	step := dag.Steps[0]

	require.NotNil(t, step.Agent)
	assert.Equal(t, "claude", step.Agent.Provider)
	assert.Equal(t, "sonnet", step.Agent.Model)
	assert.Equal(t, "Review the patch ${PATCH_ID}.", step.Agent.Prompt)
	assert.Equal(t, "Report only real defects.", step.Agent.System)
	assert.Equal(t, 20, *step.Agent.MaxToolIterations)
	assert.Equal(t, 7, step.Agent.MaxToolCalls)
	assert.Equal(t, "harness", step.ExecutorConfig.Type)
	assert.Equal(t, "claude", step.ExecutorConfig.Config["provider"])
	assert.Equal(t, int64(120_000_000_000), int64(step.Timeout))
	require.NotNil(t, step.OutputSchema)
	assert.Nil(t, step.LLM, "the step is not a chat step")

	names := map[string]bool{}
	for _, output := range step.Outputs {
		names[output.Name] = true
	}
	assert.True(t, names["verdict"], "the schema property is an output")
	assert.True(t, names["tool_calls"], "tool_calls is an output")

	require.NotNil(t, step.Agent.CLI)
	require.Len(t, step.Agent.CLI.Tools, 1)
	builtin := step.Agent.CLI.Tools[0]
	assert.Equal(t, ir.ToolEntryBuiltin, builtin.Kind)
	require.NotNil(t, builtin.Builtin)
	assert.True(t, builtin.Builtin.Read)
	assert.True(t, builtin.Builtin.Search)
	assert.False(t, builtin.Builtin.Write)
}

// A step with no key for the limits gets their defaults: 100 tool calls and no
// limit of turns.
func TestAgentRunDefaults(t *testing.T) {
	t.Parallel()

	dag, err := loadAgentRunStep(t, t.TempDir(), "{ provider: claude, endpoint: { name: test-endpoint, model: claude-test }, prompt: hello }")
	require.NoError(t, err)
	run := dag.Steps[0].Agent
	assert.Equal(t, ir.DefaultAgentMaxToolCalls, run.MaxToolCalls)
	assert.Equal(t, 100, run.MaxToolCalls)
	assert.Nil(t, run.MaxToolIterations)
	require.NotNil(t, run.CLI)
	assert.Empty(t, run.CLI.Tools)
	assert.Empty(t, run.CLI.Skills)
}

// The provider is a CLI provider: a built-in provider that is not a chat provider,
// or a harnesses entry that is not ACP. A chat provider, an ACP provider, an
// unknown provider, no provider and a value reference are validation errors. The
// error of a chat provider tells the author to use chat.completion.
func TestAgentRunProviderMustBeACLIProvider(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		with   string
		errors []string
	}{
		{"CLI provider", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test", nil},
		{"another CLI provider", "provider: codex", nil},
		{"harnesses entry", "provider: my-cli", nil},
		{"local is a chat provider", "provider: local\n      model: m", []string{"with.provider", `"local" is a chat provider`, "use chat.completion"}},
		{"anthropic is a chat provider", "provider: anthropic\n      model: m", []string{"with.provider", `"anthropic" is a chat provider`, "use chat.completion"}},
		{"gemini is a chat provider and a CLI provider", "provider: gemini", []string{"with.provider", `"gemini" is a chat provider`, "use chat.completion"}},
		{"opencode is a chat provider and a CLI provider", "provider: opencode", []string{"with.provider", `"opencode" is a chat provider`, "use chat.completion"}},
		{"no provider", "model: m", []string{"with.provider", "agent.run needs a provider; it names the CLI provider"}},
		{"unknown provider", "provider: nowhere", []string{"with.provider", `"nowhere" is not a CLI provider`}},
		{"acp provider", "provider: acp-agent", []string{"with.provider", "ACP"}},
		{"value reference", "provider: ${PROVIDER}", []string{"with.provider", "must be a literal name, not a reference"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dag, err := loadAgentRun(t, t.TempDir(), `
harnesses:
  acp-agent:
    protocol: acp
    binary: codex-acp
  my-cli:
    binary: my-agent
steps:
  - id: s
    action: agent.run
    with:
      prompt: hello
      `+tc.with+`
`)
			if len(tc.errors) > 0 {
				require.Error(t, err)
				for _, part := range tc.errors {
					assert.Contains(t, err.Error(), part)
				}
				return
			}
			require.NoError(t, err)
			require.NotNil(t, dag.Steps[0].Agent)
		})
	}
}

// An unknown key and a key that the provider cannot run fail with a message that
// names the key. An unknown key gets the same error as any other key that is not
// in the closed with, also a key of another action.
func TestAgentRunRejectsWhatTheEngineCannotRun(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		with string
		want []string
	}{
		{"unknown key", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      bogus: 1", []string{"bogus", "unknown key"}},
		{"messages is not a key", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      messages: []", []string{`unknown key "messages" in agent.run; valid keys are prompt, system, provider, endpoint, dags, tools, skills, max_tool_iterations, max_tool_calls`}},
		{"engine", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      engine: cli", []string{`unknown key "engine" in agent.run; valid keys are`}},
		{"fallback", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      fallback: [{ provider: codex }]", []string{`unknown key "fallback" in agent.run`}},
		{"cli block", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      cli: { permission_mode: plan }", []string{`unknown key "cli" in agent.run`}},
		{"on_rate_limited", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      on_rate_limited: wait", []string{`unknown key "on_rate_limited" in agent.run`}},
		{"rate_limit_wait_sec", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      rate_limit_wait_sec: 60", []string{`unknown key "rate_limit_wait_sec" in agent.run`}},
		{"max_wait_sec", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      max_wait_sec: 10", []string{`unknown key "max_wait_sec"`}},
		{"rate_limit_poll_sec", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      rate_limit_poll_sec: 10", []string{`unknown key "rate_limit_poll_sec"`}},
		{"on_auth_required", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      on_auth_required: fail", []string{`unknown key "on_auth_required"`}},
		{"environment", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      environment: box", []string{`unknown key "environment"`}},
		{"transfer", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      transfer: { max_bytes: 1 }", []string{`unknown key "transfer"`}},
		{"max_tools", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      max_tools: 4", []string{`unknown key "max_tools" in agent.run`}},
		{"tool_result_max_bytes", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      tool_result_max_bytes: 4", []string{`unknown key "tool_result_max_bytes" in agent.run`}},
		{"top-level api key", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      temperature: 0.1", []string{`unknown key "temperature" in agent.run`}},
		{"base_url", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      base_url: http://localhost:1", []string{`unknown key "base_url" in agent.run`}},
		{"api block", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      api: { temperature: 0.1 }", []string{`unknown key "api" in agent.run`}},
		{"tools as a list", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      tools:\n        - lookup", []string{"with.tools", "list form of tools is valid on chat.completion only", "key dags"}},
		{"tools as a string", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      tools: lookup", []string{"with.tools", "tools must be a map with the categories mcp, builtin"}},
		{"dags in the tool map", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      tools:\n        dags:\n          - name: lookup", []string{"with.tools.dags", `unknown key "dags" in the tool map; valid keys are mcp, builtin`}},
		{"dags as a map", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      dags:\n        name: lookup", []string{"with.dags", "dags must be a list of entries, got a map"}},
		{"dags as null", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      dags:", []string{"with.dags", "dags must be a list of entries, got no value"}},
		{"unknown category", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      tools:\n        toolsets:\n          - name: support", []string{"with.tools.toolsets", `unknown key "toolsets" in the tool map; valid keys are mcp, builtin`}},
		{"mcp named dagu", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      dags:\n        - name: lookup\n      tools:\n        mcp:\n          - { name: dagu, url: \"http://127.0.0.1:1\", allow: [a] }", []string{"with.tools.mcp[0].name", "dagu", "tool bridge"}},
		{"mcp without a name", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      tools:\n        mcp:\n          - { url: \"http://127.0.0.1:1\", allow: [search] }", []string{"with.tools.mcp[0].name", "name is required"}},
		{"two mcp entries with one name", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      tools:\n        mcp:\n          - { name: docs, url: \"http://127.0.0.1:1\", allow: [a] }\n          - { name: docs, url: \"http://127.0.0.1:2\", allow: [b] }", []string{"with.tools.mcp[1].name", "docs", "unique"}},
		{"actions category", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      tools:\n        actions:\n          - name: ops.notify", []string{"with.tools.actions", `unknown key "actions" in the tool map; valid keys are mcp, builtin`}},
		{"duplicate tool name", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      dags:\n        - name: lookup\n        - name: lookup", []string{"with.dags[1]", `tool name "lookup" is also the tool name of with.dags[0]`}},
		{"unknown skill on cli", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      skills: [nothing]", []string{"nothing"}},
		{"shell with no allow", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      tools:\n        builtin: { shell: { deny: [rm] } }", []string{"allow"}},
		{"shell true", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      tools:\n        builtin: { shell: true }", []string{"shell", "true"}},
		{"star pattern", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      tools:\n        builtin: { shell: { allow: ['*'] } }", []string{"pattern"}},
		{"unknown builtin key", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      tools:\n        builtin: { delete: true }", []string{"with.tools.builtin.delete", `unknown key "delete" in builtin; valid keys are read, write, edit, search, shell`}},
		{"max_tool_calls below 1", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      max_tool_calls: 0", []string{"with.max_tool_calls", "max_tool_calls must be an integer of at least 1"}},
		{"rate_limit_wait_sec is not a key", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      rate_limit_wait_sec: 5", []string{`unknown key "rate_limit_wait_sec"`}},
		{"max_tool_iterations below 1", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      max_tool_iterations: 0", []string{"with.max_tool_iterations", "at least 1"}},
		{"max_turns is not a key", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      max_turns: 3", []string{`unknown key "max_turns" in agent.run`, "max_tool_iterations"}},
		{"agent is not a key", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      agent: reviewer", []string{`unknown key "agent" in agent.run`, "valid keys are"}},
		{"tool entry with if", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      dags:\n        - name: lookup\n          if: [{ condition: x, expected: y }]", []string{"with.dags[0].if", `unknown key "if" in an entry of dags; valid keys are name, params`}},
		{"mcp entry with if", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      tools:\n        mcp:\n          - { name: docs, url: \"http://127.0.0.1:1\", allow: [a], if: [{ condition: x, expected: y }] }", []string{"with.tools.mcp[0].if", `unknown key "if" in an entry of mcp; valid keys are name, url, headers, allow`}},
		{"other CLI provider with system", "provider: codex\n      system: be brief", []string{`key "system" is not supported by the provider "codex"`, "only model, prompt and timeout_sec"}},
		{"other CLI provider with max_tool_calls", "provider: codex\n      max_tool_calls: 5", []string{`key "max_tool_calls" is not supported by the provider "codex"`}},
		{"other CLI provider with tools", "provider: codex\n      tools:\n        mcp:\n          - { name: docs, url: \"http://127.0.0.1:1\", allow: [a] }", []string{`key "tools" is not supported by the provider "codex"`}},
		{"other CLI provider with dags", "provider: codex\n      dags:\n        - name: lookup", []string{`key "dags" is not supported by the provider "codex"`}},
		{"effort not in the set", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n        effort: extreme", []string{"with.endpoint.effort", "effort must be one of low, medium, high, xhigh, max", "extreme"}},
		{"effort in capitals", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n        effort: High", []string{"with.endpoint.effort", "effort must be one of low, medium, high, xhigh, max"}},
		{"effort that is not a string", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n        effort: 3", []string{"with.endpoint.effort", "effort must be one of low, medium, high, xhigh, max"}},
		{"other CLI provider with effort", "provider: codex\n      effort: high", []string{"with.effort", `unknown key "effort" in agent.run; valid keys are prompt, system, provider, model, endpoint`}},
		{"other CLI provider with an endpoint block", "provider: codex\n      endpoint: { name: cle1, model: m }", []string{"with.endpoint", `key "endpoint" is not supported by the provider "codex"`}},
		{"missing prompt", "provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      prompt: \"\"", []string{"prompt"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writeSkill(t, dir, "refund-policy", "---\nname: refund-policy\ndescription: Rules\n---\nRefund in 30 days.\n")
			text := "prompt: hello\n      " + tc.with
			if tc.name == "missing prompt" {
				text = tc.with
			}
			_, err := loadAgentRunStep(t, dir, text)
			require.Error(t, err)
			for _, part := range tc.want {
				assert.Contains(t, err.Error(), part)
			}
		})
	}
}

// The key max_tool_iterations has the name and the range that chat.completion
// has on main. A chat.completion step keeps it, and a step with an agent key
// fails as on main: the key does not exist on that action.
func TestChatCompletionKeepsMaxToolIterations(t *testing.T) {
	t.Parallel()

	dag, err := loadAgentRun(t, t.TempDir(), `
steps:
  - id: s
    action: chat.completion
    with:
      provider: local
      model: m
      prompt: hi
      max_tool_iterations: 3
`)
	require.NoError(t, err)
	require.NotNil(t, dag.Steps[0].LLM.MaxToolIterations)
	assert.Equal(t, 3, *dag.Steps[0].LLM.MaxToolIterations)
	assert.Nil(t, dag.Steps[0].Agent, "a chat.completion step is not an agent.run step")

	_, err = loadAgentRun(t, t.TempDir(), `
steps:
  - id: s
    action: chat.completion
    with:
      provider: local
      model: m
      prompt: hi
      agent: support
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent")
}

// The closed schema rules are checked at load.
func TestAgentRunOutputSchemaMustBeClosed(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		schema string
		want   string
	}{
		{"open top level", "type: object\n      properties: { a: { type: string } }", "additionalProperties"},
		{"open nested object", "type: object\n      additionalProperties: false\n      properties:\n        a: { type: object, properties: { b: { type: string } } }", "additionalProperties"},
		{"open array item", "type: object\n      additionalProperties: false\n      properties:\n        a: { type: array, items: { type: object, properties: { b: { type: string } } } }", "additionalProperties"},
		{"required not listed", "type: object\n      additionalProperties: false\n      properties: { a: { type: string } }\n      required: [b]", "required"},
		{"tool_calls property", "type: object\n      additionalProperties: false\n      properties: { tool_calls: { type: array } }", "tool_calls"},
		{"not an object", "type: string", "type: object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := loadAgentRun(t, t.TempDir(), `
steps:
  - id: s
    action: agent.run
    output_schema:
      `+tc.schema+`
    with: { provider: claude, endpoint: { name: test-endpoint, model: claude-test }, prompt: hi }
`)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// An output_schema above 100000 bytes is a validation error.
func TestAgentRunOutputSchemaSizeLimit(t *testing.T) {
	t.Parallel()

	big := "type: object\n      additionalProperties: false\n      properties:\n        a: { type: string, description: \"" + strings.Repeat("x", 100000) + "\" }"
	_, err := loadAgentRun(t, t.TempDir(), `
steps:
  - id: s
    action: agent.run
    output_schema:
      `+big+`
    with: { provider: claude, endpoint: { name: test-endpoint, model: claude-test }, prompt: hi }
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "above the limit of 100000 bytes")

	_, err = loadAgentRun(t, t.TempDir(), `
steps:
  - id: s
    action: agent.run
    output_schema:
      type: object
      additionalProperties: false
      properties:
        a: { type: string, description: "`+strings.Repeat("x", 90000)+`" }
    with: { provider: claude, endpoint: { name: test-endpoint, model: claude-test }, prompt: hi }
`)
	require.NoError(t, err, "a schema below the limit loads")
}

// A reference to a DAG secret in the prompt or the system text is an error, in
// each form that Dagu expands. A dollar escaped with a backslash is not a
// reference. A double dollar is: the second dollar starts a reference.
func TestAgentRunRejectsASecretInThePrompt(t *testing.T) {
	t.Parallel()

	for _, reference := range []string{"${API_TOKEN}", "$API_TOKEN", "${env.API_TOKEN}", "${API_TOKEN:-none}", "$$API_TOKEN"} {
		_, err := loadAgentRun(t, t.TempDir(), `
secrets:
  - name: API_TOKEN
    provider: env
    key: SOME_TOKEN
steps:
  - id: s
    action: agent.run
    with:
      provider: claude
      endpoint: { name: test-endpoint, model: claude-test }
      prompt: "use `+reference+` now"
`)
		require.Error(t, err, reference)
		assert.Contains(t, err.Error(), "with.prompt", reference)
		assert.Contains(t, err.Error(), `prompt references the DAG secret "API_TOKEN"`, reference)
	}

	_, err := loadAgentRun(t, t.TempDir(), `
secrets:
  - name: API_TOKEN
    provider: env
    key: SOME_TOKEN
steps:
  - id: s
    action: agent.run
    with:
      provider: claude
      endpoint: { name: test-endpoint, model: claude-test }
      prompt: 'the text \$API_TOKEN is not a reference; nor is $API_TOKEN_ID'
`)
	require.NoError(t, err)

	for _, field := range []string{"prompt", "system"} {
		_, err := loadAgentRun(t, t.TempDir(), `
secrets:
  - name: API_TOKEN
    provider: env
    key: SOME_TOKEN
steps:
  - id: s
    action: agent.run
    with:
      provider: claude
      endpoint:
        name: test-endpoint
        model: claude-test
      prompt: hello
      `+field+`: "use ${API_TOKEN} now"
`)
		require.Error(t, err, field)
		assert.Contains(t, err.Error(), "API_TOKEN")
	}
}

// A step accepts the category builtin and mcp entries, and keeps them in
// the compiled form, with the shell patterns as the author wrote them. The
// value of an mcp entry stays a template until the step runs.
func TestAgentRunKeepsTheBuiltinToolsAndMCP(t *testing.T) {
	t.Parallel()

	dag, err := loadAgentRun(t, t.TempDir(), `
steps:
  - id: ask
    action: agent.run
    with:
      provider: claude
      endpoint:
        name: test-endpoint
        model: claude-test
      prompt: hello
      max_tool_calls: 5
      tools:
        builtin:
          read: true
          shell: { allow: ['git diff *'], deny: ['git push *'] }
        mcp:
          - name: docs
            url: ${DOCS_URL}
            headers: { Authorization: "Bearer ${DOCS_TOKEN}" }
            allow: [search]
`)
	require.NoError(t, err)
	run := dag.Steps[0].Agent
	assert.Equal(t, 5, run.MaxToolCalls)
	cli := run.CLI
	require.NotNil(t, cli)
	require.Len(t, cli.Tools, 2)
	assert.Equal(t, ir.ToolEntryMCP, cli.Tools[0].Kind, "the entries of the lists come before the builtin entry")
	assert.Equal(t, "docs", cli.Tools[0].Name)
	assert.Equal(t, "${DOCS_URL}", cli.Tools[0].MCP.URL)
	builtin := cli.Tools[1].Builtin
	require.NotNil(t, builtin)
	assert.True(t, builtin.Read)
	assert.Equal(t, []string{"git diff *"}, builtin.Shell.Allow)
	assert.Equal(t, []string{"git push *"}, builtin.Shell.Deny)
}

// An mcp entry with no url names a MCP server entry of the AI page. The step keeps
// it with the key allow and no URL, and the DAG needs the capability of the relay.
func TestAgentRunKeepsANamedMCPEntry(t *testing.T) {
	t.Parallel()

	dag, err := loadAgentRun(t, t.TempDir(), `
steps:
  - id: ask
    action: agent.run
    with:
      provider: claude
      endpoint:
        name: test-endpoint
        model: claude-test
      prompt: hello
      tools:
        mcp:
          - { name: docs, allow: [search] }
          - { name: other, url: "http://127.0.0.1:1", allow: [x] }
`)
	require.NoError(t, err)
	step := dag.Steps[0]
	assert.Equal(t, []string{"docs"}, step.AIMCPServers())
	assert.Equal(t, []string{"search"}, step.MCPAllow("docs"))
	assert.Equal(t, []string{ir.CapabilityAIRelay}, dag.RequiredCapabilities())

	for name, tools := range map[string]string{
		"headers":  "          - { name: docs, headers: { Authorization: x }, allow: [search] }",
		"no allow": "          - { name: docs }",
	} {
		_, err := loadAgentRun(t, t.TempDir(), "steps:\n  - id: ask\n    action: agent.run\n    with:\n      provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      prompt: hello\n      tools:\n        mcp:\n"+tools+"\n")
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "with.tools.mcp[0]", name)
	}
}

// A step keeps the Dagu tools for the tool bridge: the entries of dags, in the
// order written, next to the mcp entries and the builtin entry.
func TestAgentRunKeepsTheDaguTools(t *testing.T) {
	t.Parallel()

	dag, err := loadAgentRun(t, t.TempDir(), `
steps:
  - id: ask
    action: agent.run
    with:
      provider: claude
      endpoint:
        name: test-endpoint
        model: claude-test
      prompt: hello
      tools:
        builtin: { read: true }
        mcp:
          - { name: docs, url: "http://127.0.0.1:1", allow: [search] }
      dags:
        - name: lookup
        - name: refund
          params:
            CUSTOMER_ID: c-42
            AMOUNT: { from_ai: true }
`)
	require.NoError(t, err)
	cli := dag.Steps[0].Agent.CLI
	require.NotNil(t, cli)

	kinds := make([]ir.ToolEntryKind, 0, len(cli.Tools))
	for _, entry := range cli.Tools {
		kinds = append(kinds, entry.Kind)
	}
	assert.Equal(t, []ir.ToolEntryKind{
		ir.ToolEntryDAG, ir.ToolEntryDAG, ir.ToolEntryMCP, ir.ToolEntryBuiltin,
	}, kinds)
	assert.Equal(t, "lookup", cli.Tools[0].DAG)
	assert.Equal(t, "refund", cli.Tools[1].DAG)
	assert.Equal(t, "c-42", cli.Tools[1].Params["CUSTOMER_ID"].Value)
	assert.True(t, cli.Tools[1].Params["AMOUNT"].FromAI)
}

// A reference to the output of another step in the prompt orders the steps.
func TestAgentRunPromptReferencesInferDependencies(t *testing.T) {
	t.Parallel()

	dag, err := loadAgentRun(t, t.TempDir(), `
steps:
  - id: fetch
    run: printf 'TITLE=hello\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: TITLE
  - id: review
    action: agent.run
    with:
      prompt: "Review ${steps.fetch.outputs.TITLE}."
      provider: claude
      endpoint:
        name: test-endpoint
        model: claude-test
      system: "Context ${steps.fetch.outputs.TITLE}"
`)
	require.NoError(t, err)
	review := dag.Steps[1]
	require.NotEmpty(t, review.InferredDepends)
	assert.Equal(t, "fetch", review.InferredDepends[0].Step)
}

// An agent.run step does not run in a container.
func TestAgentRunRejectsAContainer(t *testing.T) {
	t.Parallel()

	_, err := loadAgentRun(t, t.TempDir(), `
steps:
  - id: review
    action: agent.run
    container:
      image: alpine
    with: { provider: claude, endpoint: { name: test-endpoint, model: claude-test }, prompt: hello }
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "container")
	assert.Contains(t, err.Error(), "does not run in a container")
}

// A with of agent.run never reaches the CLI as a free flag: the compiled step
// holds the provider and the model only.
func TestAgentRunConfigHoldsNoFreeFlags(t *testing.T) {
	t.Parallel()

	dag, err := loadAgentRunStep(t, t.TempDir(), "{ provider: claude, endpoint: { name: test-endpoint, model: sonnet }, prompt: hello, max_tool_iterations: 3, max_tool_calls: 4 }")
	require.NoError(t, err)
	config := dag.Steps[0].ExecutorConfig.Config
	assert.Equal(t, "claude", config["provider"])
	assert.Equal(t, "sonnet", config["model"])
	for _, key := range []string{"max_tool_iterations", "max_tool_calls", "permission_mode", "prompt", "fallback"} {
		assert.NotContains(t, config, key)
	}
}

// The key effort is one of the five levels of the CLI. A reference such as
// ${EFFORT} passes, as it does for model, and the harness checks the value when
// the step runs.
func TestAgentRunEffort(t *testing.T) {
	t.Parallel()

	for _, level := range []string{"low", "medium", "high", "xhigh", "max", "${EFFORT}"} {
		dag, err := loadAgentRunStep(t, t.TempDir(), "{ provider: claude, endpoint: { name: test-endpoint, model: sonnet, effort: \""+level+"\" }, prompt: hello }")
		require.NoError(t, err, level)
		assert.Equal(t, level, dag.Steps[0].ExecutorConfig.Config["effort"], level)
	}

	dag, err := loadAgentRunStep(t, t.TempDir(), "{ provider: claude, endpoint: { name: test-endpoint, model: claude-test }, prompt: hello }")
	require.NoError(t, err)
	assert.NotContains(t, dag.Steps[0].ExecutorConfig.Config, "effort", "a step with no effort passes no flag")
}

// The key endpoint (Spec 094) is the literal name of an AI endpoint. The loader
// keeps it in the compiled form and does not pass it to the harness. It reads no
// store, so it cannot tell if the endpoint exists.
func TestAgentRunEndpoint(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"cle1", "a", "my-endpoint-2", "a" + strings.Repeat("b", 63)} {
		dag, err := loadAgentRunStep(t, t.TempDir(), "{ provider: claude, prompt: hello, endpoint: { name: "+name+", model: claude-test } }")
		require.NoError(t, err, name)
		step := dag.Steps[0]
		assert.Equal(t, name, step.Agent.Endpoint)
		assert.NotContains(t, step.ExecutorConfig.Config, "endpoint", "the harness config has no endpoint")
	}
}

// A step of the CLI claude with no endpoint is a validation error at load: the
// model key lives in the server, so the step cannot call a model without an
// endpoint. A CLI provider that no endpoint serves stays valid with none.
func TestAgentRunEndpointRequired(t *testing.T) {
	t.Parallel()

	_, err := loadAgentRunStep(t, t.TempDir(), "{ provider: claude, prompt: hello }")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "with.endpoint")
	assert.Contains(t, err.Error(), "agent.run needs an endpoint; add one on the AI page and name it here")

	dag, err := loadAgentRunStep(t, t.TempDir(), "{ provider: codex, prompt: hello }")
	require.NoError(t, err)
	assert.Empty(t, dag.Steps[0].Agent.Endpoint)
}

// The key endpoint of a step of the CLI claude is a block with the keys name,
// model and effort. A value of another type, a missing key and an unknown key
// are validation errors that name the path of the key.
func TestAgentRunEndpointBlockErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		with string
		want []string
	}{
		{"string", "endpoint: test-endpoint", []string{"field 'with.endpoint'", "endpoint must be a block with name and model"}},
		{"list", "endpoint: [test-endpoint]", []string{"field 'with.endpoint'", "endpoint must be a block with name and model"}},
		{"number", "endpoint: 3", []string{"field 'with.endpoint'", "endpoint must be a block with name and model"}},
		{"null", "endpoint: null", []string{"field 'with.endpoint'", "endpoint must be a block with name and model"}},
		{"empty block", "endpoint: {}", []string{"with.endpoint.name", "agent.run needs the name of an endpoint", "with.endpoint.model", "agent.run needs a model; choose one that the endpoint lists"}},
		{"no name", "endpoint: { model: m }", []string{"with.endpoint.name", "agent.run needs the name of an endpoint"}},
		{"no model", "endpoint: { name: cle1 }", []string{"with.endpoint.model", "agent.run needs a model; choose one that the endpoint lists"}},
		{"effort without a model", "endpoint: { name: cle1, effort: high }", []string{"with.endpoint.model", "agent.run needs a model"}},
		{"unknown key", "endpoint: { name: cle1, model: m, temperature: 1 }", []string{"with.endpoint.temperature", `unknown key "temperature" in the block endpoint; valid keys are name, model, effort`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := loadAgentRunStep(t, t.TempDir(), "prompt: hello\n      provider: claude\n      "+tc.with)
			require.Error(t, err)
			for _, part := range tc.want {
				assert.Contains(t, err.Error(), part)
			}
		})
	}
}

// The model of a step of the CLI claude MUST be in the endpoint block, because
// the model must be one that the endpoint lists. A value that is not a non-empty
// string is an error. A reference passes, as it does for the effort. Another CLI
// provider needs no model.
func TestAgentRunModelOfTheEndpoint(t *testing.T) {
	t.Parallel()

	for name, model := range map[string]string{
		"empty":  "''",
		"blank":  "' '",
		"null":   "null",
		"number": "3",
		"list":   "[a]",
	} {
		_, err := loadAgentRunStep(t, t.TempDir(), "{ provider: claude, endpoint: { name: test-endpoint, model: "+model+" }, prompt: hello }")
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "with.endpoint.model", name)
	}

	dag, err := loadAgentRunStep(t, t.TempDir(), "{ provider: claude, endpoint: { name: test-endpoint, model: '${MODEL}' }, prompt: hello }")
	require.NoError(t, err)
	assert.Equal(t, "${MODEL}", dag.Steps[0].Agent.Model)
	assert.Equal(t, "${MODEL}", dag.Steps[0].ExecutorConfig.Config["model"])

	dag, err = loadAgentRunStep(t, t.TempDir(), "{ provider: codex, prompt: hello }")
	require.NoError(t, err)
	assert.Empty(t, dag.Steps[0].Agent.Model)

	dag, err = loadAgentRunStep(t, t.TempDir(), "{ provider: codex, model: gpt-x, prompt: hello }")
	require.NoError(t, err)
	assert.Equal(t, "gpt-x", dag.Steps[0].Agent.Model, "the flat model stays valid for another CLI provider")
}

// The provider claude takes the model and the effort in the block endpoint. At
// the level of the with, they are unknown keys, as any key that the closed with
// does not have.
func TestAgentRunModelAndEffortOfTheWithAreUnknownKeys(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		with string
		want []string
	}{
		{"model", "endpoint: { name: cle1, model: m }\n      model: m", []string{"with.model", `unknown key "model" in agent.run; valid keys are prompt, system, provider, endpoint, dags, tools`}},
		{"effort", "endpoint: { name: cle1, model: m }\n      effort: high", []string{"with.effort", `unknown key "effort" in agent.run`}},
		{"model and a string endpoint", "endpoint: cle1\n      model: m", []string{"with.model", `unknown key "model" in agent.run`}},
		{"model without an endpoint", "model: m", []string{"with.model", `unknown key "model" in agent.run`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := loadAgentRunStep(t, t.TempDir(), "prompt: hello\n      provider: claude\n      "+tc.with)
			require.Error(t, err)
			for _, part := range tc.want {
				assert.Contains(t, err.Error(), part)
			}
		})
	}
}

// The name in the endpoint block is a literal name of an AI endpoint. A value
// that is not one is a validation error that names the key.
func TestAgentRunEndpointErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		with string
		want []string
	}{
		{"number", "endpoint: { name: 3, model: m }", []string{"with.endpoint.name", "name must be a non-empty string"}},
		{"list", "endpoint: { name: [cle1], model: m }", []string{"with.endpoint.name", "non-empty string"}},
		{"bool", "endpoint: { name: true, model: m }", []string{"with.endpoint.name", "non-empty string"}},
		{"empty", "endpoint: { name: '', model: m }", []string{"with.endpoint.name", "non-empty string"}},
		{"null", "endpoint: { name: null, model: m }", []string{"with.endpoint.name", "non-empty string"}},
		{"upper case", "endpoint: { name: Cle1, model: m }", []string{"with.endpoint.name", "name must match ^[a-z][a-z0-9-]{0,63}$"}},
		{"leading digit", "endpoint: { name: 1cle, model: m }", []string{"with.endpoint.name", "must match"}},
		{"underscore", "endpoint: { name: my_key, model: m }", []string{"with.endpoint.name", "must match"}},
		{"slash", "endpoint: { name: a/b, model: m }", []string{"with.endpoint.name", "must match"}},
		{"space", "endpoint: { name: ' cle1', model: m }", []string{"with.endpoint.name", "must match"}},
		{"too long", "endpoint: { name: a" + strings.Repeat("b", 64) + ", model: m }", []string{"with.endpoint.name", "must match"}},
		{"braced reference", "endpoint: { name: '${ENDPOINT}', model: m }", []string{"with.endpoint.name", "literal name, not a reference"}},
		{"plain reference", "endpoint: { name: '$ENDPOINT', model: m }", []string{"with.endpoint.name", "literal name, not a reference"}},
		{"another provider", "provider: codex\n      endpoint: { name: cle1, model: m }", []string{`key "endpoint" is not supported by the provider "codex"`, "claude"}},
		{"another provider with a string", "provider: codex\n      endpoint: cle1", []string{`key "endpoint" is not supported by the provider "codex"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			with := "prompt: hello\n      provider: claude\n      " + tc.with
			if strings.HasPrefix(tc.with, "provider:") {
				with = "prompt: hello\n      " + tc.with
			}
			_, err := loadAgentRunStep(t, t.TempDir(), with)
			require.Error(t, err)
			for _, part := range tc.want {
				assert.Contains(t, err.Error(), part)
			}
		})
	}
}
