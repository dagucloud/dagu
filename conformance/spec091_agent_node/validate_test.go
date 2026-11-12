// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec091_agent_node_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// dagu validate rejects what the provider cannot run, with a message that names
// the key. A key that agent.run does not have is an unknown key, and the message
// lists the valid keys. A chat provider is not a CLI provider: the message tells
// the author to use chat.completion.
func TestValidateRejectsWhatTheProviderCannotRun(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		fixture string
		message []string
	}{
		// A key that the provider cannot run.
		{"invalid_other_cli_provider_system.yaml", []string{`key "system" is not supported by the provider "codex"`, "only model, prompt and timeout_sec"}},
		{"invalid_other_cli_provider_effort.yaml", []string{"with.effort", `"effort"`}},
		{"invalid_effort.yaml", []string{"with.endpoint.effort", "effort must be one of low, medium, high, xhigh, max", "extreme"}},
		{"invalid_other_cli_provider_max_tool_calls.yaml", []string{`key "max_tool_calls" is not supported by the provider "codex"`}},
		// The provider is a CLI provider.
		{"invalid_no_provider.yaml", []string{"agent.run needs a provider", "names the CLI provider"}},
		{"invalid_chat_provider.yaml", []string{"with.provider", `"anthropic" is a chat provider`, "use chat.completion"}},
		{"invalid_unknown_provider.yaml", []string{`"nowhere" is not a CLI provider or a harnesses entry`}},
		{"invalid_acp_provider.yaml", []string{"ACP harness", "agent.run does not run"}},
		{"invalid_provider_reference.yaml", []string{"provider must be a literal name, not a reference"}},
		// The provider claude needs an endpoint: the DAG does not load without one.
		{"invalid_no_endpoint.yaml", []string{"with.endpoint", "agent.run needs an endpoint; add one on the AI page and name it here"}},
		// The provider claude needs a model: the model must be one that the endpoint lists.
		{"invalid_no_model.yaml", []string{"with.endpoint.model", "agent.run needs a model; choose one that the endpoint lists"}},
		// The endpoint is one block with name, model and effort.
		{"invalid_endpoint_string.yaml", []string{"with.endpoint", "endpoint must be a block with name and model"}},
		{"invalid_endpoint_no_name.yaml", []string{"with.endpoint.name", "agent.run needs the name of an endpoint"}},
		{"invalid_endpoint_unknown_key.yaml", []string{"with.endpoint.temperature", `unknown key "temperature"`, "name, model, effort"}},
		// Skills.
		{"invalid_unknown_skill.yaml", []string{"with.skills", "nothing"}},
		{"invalid_skills_not_a_list.yaml", []string{"with.skills", "skills must be a list of skill names, got a string"}},
		// Ranges.
		{"invalid_max_tool_calls_zero.yaml", []string{"max_tool_calls must be an integer of at least 1"}},
		// Tool entries. The list form of tools is for chat.completion only.
		{"invalid_tools_as_list.yaml", []string{"with.tools", "list form of tools is valid on chat.completion only", "key dags"}},
		{"invalid_dags_in_tool_map.yaml", []string{"with.tools.dags", `unknown key "dags" in the tool map; valid keys are mcp, builtin`}},
		{"invalid_tools_list_form.yaml", []string{"with.tools", "list form of tools is valid on chat.completion only", "deprecated"}},
		{"invalid_mcp_without_name.yaml", []string{"with.tools.mcp[0].name", "name is required"}},
		{"invalid_mcp_duplicate_name.yaml", []string{"with.tools.mcp[1].name", `"docs"`, "unique"}},
		{"invalid_mcp_named_dagu.yaml", []string{"with.tools.mcp[0].name", `"dagu"`, "tool bridge"}},
		{"invalid_duplicate_tool_name.yaml", []string{"with.dags[1]", `tool name "lookup" is also the tool name of with.dags[0]`}},
		{"invalid_shell_true.yaml", []string{"shell", "true"}},
		{"invalid_shell_without_allow.yaml", []string{"allow"}},
		{"invalid_shell_star_pattern.yaml", []string{"command pattern"}},
		{"invalid_builtin_unknown_key.yaml", []string{"with.tools.builtin.delete", `"delete"`, "read, write, edit, search"}},
		// Other keys.
		{"invalid_unknown_key.yaml", []string{"with.bogus", `unknown key "bogus"`, "valid keys are"}},
		{"invalid_no_prompt.yaml", []string{"needs a prompt"}},
		{"invalid_secret_in_prompt.yaml", []string{"API_TOKEN", "must not enter a prompt"}},
		// The typed result.
		{"invalid_open_schema.yaml", []string{"additionalProperties: false"}},
		{"invalid_nested_open_schema.yaml", []string{"additionalProperties: false", "item"}},
		{"invalid_tool_calls_property.yaml", []string{"tool_calls"}},
		{"invalid_required_not_listed.yaml", []string{`names "other"`}},
		{"invalid_max_tool_iterations_zero.yaml", []string{"with.max_tool_iterations", "at least 1"}},
		{"invalid_tool_unknown_key.yaml", []string{"with.dags[0].retries", `"retries"`, "name, params"}},
		{"invalid_mcp_tool_unknown_key.yaml", []string{"with.tools.mcp[0].retries", `"retries"`, "name, url, headers, allow"}},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.fixture)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.message...)
		})
	}
}

// A valid agent.run step passes dagu validate.
func TestValidateAcceptsAgentRun(t *testing.T) {
	t.Parallel()

	for _, fixture := range []string{
		"cli_review.yaml", "cli_flags.yaml", "cli_minimal.yaml", "cli_effort.yaml", "cli_effort_reference.yaml", "cli_other_provider.yaml",
		"cli_rate_retry_ok.yaml", "skills_cli.yaml",
		"bridge_catalog.yaml", "cli_all_tool_kinds.yaml", "chat_max_tool_iterations.yaml",
	} {
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()

			project := newProject(t, nil)
			project.dagu.RunWithEnv(project.env("quiet"), "validate", fixture).ExpectExitCode(0)
		})
	}
}

// The key max_tool_iterations of a chat.completion step keeps the meaning of
// Spec 059: the step stops the tool loop after that many requests and still
// succeeds. The tools of its map still run.
func TestMaxToolIterationsOfChatCompletion(t *testing.T) {
	t.Parallel()

	// The model asks for a tool that does not exist, again and again.
	model := newLLM(t, toolCallReply("unknown-tool", `{}`, 3, 1))
	project := newProject(t, model)
	project.start("quiet", "chat_max_tool_iterations.yaml").ExpectExitCode(0)

	require.Len(t, model.captured(), 2)
	node := project.savedNode("ask")
	require.Nil(t, node.Step.AgentRun, "a chat.completion step is not an agent.run step")
	require.Equal(t, "chat", node.Step.ExecutorConfig.Type)
}
