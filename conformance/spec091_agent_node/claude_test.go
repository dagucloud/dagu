// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec091_agent_node_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/require"
)

// The command of the provider claude has the flags of Spec 091 in their order.
// One flag, --allowedTools, has many arguments. The permission mode is fixed to
// dontAsk, and Dagu passes no --bare. The system text goes to
// --append-system-prompt, the MCP config goes to --mcp-config, and the schema goes
// to --json-schema as one line.
func TestClaudeFlags(t *testing.T) {
	t.Parallel()

	project := newProject(t, nil)
	project.start("quiet", "cli_flags.yaml").ExpectExitCode(0)

	argv := project.argv(1)
	mcpConfig := flagValue(t, argv, "--mcp-config")
	require.Equal(t, []string{
		"-p", "Review the patch p-42.",
		"--output-format", "stream-json", "--verbose",
		"--no-session-persistence",
		"--permission-mode", "dontAsk",
		"--model", "claude-test",
		"--append-system-prompt", "Be brief.",
		"--tools", "Read,Grep,Glob,Bash",
		"--allowedTools", "Read", "Grep", "Glob", "Bash(git diff *)", "mcp__docs__search",
		"--disallowedTools", "Bash(git push *)",
		"--strict-mcp-config", "--mcp-config", mcpConfig,
		"--max-turns", "7",
		"--json-schema", `{"additionalProperties":false,"properties":{"verdict":{"enum":["accept","reject"],"type":"string"}},"required":["verdict"],"type":"object"}`,
	}, argv)
	require.NotContains(t, argv, "--bare")

	// The MCP config has the server of the mcp entry. Only the owner reads it, and
	// Dagu deletes it at the end of the attempt.
	require.JSONEq(t,
		`{"mcpServers":{"docs":{"type":"http","url":"http://127.0.0.1:1/mcp","headers":{"Authorization":"Bearer abc"}}}}`,
		project.fakeFile("mcp.1.json"))
	require.Equal(t, "-r--------", strings.TrimSpace(project.fakeFile("mcp.1.mode")))
	_, err := os.Stat(mcpConfig)
	require.True(t, os.IsNotExist(err), "the MCP config is deleted after the attempt")
}

// The key effort gives --effort right after --model. A reference in the key
// resolves before the flag is built. The harness does not check that the model
// supports the level.
func TestClaudeEffortFlag(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		fixture string
		flags   []string
	}{
		{"cli_effort.yaml", []string{"--model", "claude-test", "--effort", "high"}},
		{"cli_effort_reference.yaml", []string{"--effort", "max"}},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()

			project := newProject(t, nil)
			project.start("quiet", tc.fixture).ExpectExitCode(0)

			argv := project.argv(1)
			i := slices.Index(argv, tc.flags[0])
			require.GreaterOrEqual(t, i, 0)
			require.Equal(t, tc.flags, argv[i:i+len(tc.flags)])
		})
	}
}

// A reference that resolves to a value outside the five levels fails the attempt
// before the CLI starts.
func TestClaudeEffortReferenceOutOfSet(t *testing.T) {
	t.Parallel()

	project := newProject(t, nil)
	requirePartialSuccess(t, project.start("quiet", "cli_effort_invalid.yaml"))

	project.dagu.ExpectTextFileContent("after.out", "continued\n")
	require.Equal(t, 0, project.calls(), "the fake claude did not run")
}

// A flag is absent when its key is absent. --model is always present, because the
// step must name a model. --tools is always present, and it is
// empty when the step has no built-in tool. The MCP flags are always present.
func TestClaudeFlagsMinimal(t *testing.T) {
	t.Parallel()

	project := newProject(t, nil)
	project.start("quiet", "cli_minimal.yaml").ExpectExitCode(0)

	argv := project.argv(1)
	mcpConfig := flagValue(t, argv, "--mcp-config")
	require.Equal(t, []string{
		"-p", "Say hello.",
		"--output-format", "stream-json", "--verbose",
		"--no-session-persistence",
		"--permission-mode", "dontAsk",
		"--model", "claude-test",
		"--tools", "",
		"--strict-mcp-config", "--mcp-config", mcpConfig,
	}, argv)
	require.JSONEq(t, `{"mcpServers":{}}`, project.fakeFile("mcp.1.json"))
}

// The typed result is structured_output of the last result message. The step
// writes it as one line of JSON, and publishes its properties and tool_calls with
// the normalized names (mcp__docs__search gives docs_search). A call of
// StructuredOutput is not in the list.
func TestClaudeTypedResultAndToolCalls(t *testing.T) {
	t.Parallel()

	project := newProject(t, nil)
	project.start("ok", "cli_ok.yaml").ExpectExitCode(0)

	project.dagu.ExpectTextFileContent("review.out", `{"verdict":"accept"}`+"\n")
	project.dagu.ExpectTextFileContent("show.out", `verdict=accept calls=["search","docs_search","shell"]`+"\n")
}

// The saved DAG holds the agent.run step with its provider, its model and its
// prompt, and the compiled step runs as harness.run. The step publishes the typed
// property and tool_calls.
func TestSavedFormOfAnAgentRun(t *testing.T) {
	t.Parallel()

	project := newProject(t, nil)
	project.start("quiet", "cli_review.yaml").ExpectExitCode(0)

	project.dagu.ExpectTextFileContent("show.out", "verdict=accept calls=[]\n")
	project.dagu.ExpectTextFileContent("review.out", `{"verdict":"accept"}`+"\n")

	node := project.savedNode("review")
	require.NotNil(t, node.Step.AgentRun)
	require.Equal(t, "claude", node.Step.AgentRun.Provider)
	require.Equal(t, "claude-test", node.Step.AgentRun.Model)
	require.Equal(t, "Review the patch ${PATCH_ID}.", node.Step.AgentRun.Prompt)
	require.Equal(t, "harness", node.Step.ExecutorConfig.Type)
}

// A step with no output_schema has a text result: the field result of the last
// result message. Dagu reads no JSON from it, and ignores a line of the stream
// that is not JSON.
func TestClaudeTextResult(t *testing.T) {
	t.Parallel()

	project := newProject(t, nil)
	project.start("text", "cli_text.yaml").ExpectExitCode(0)

	project.dagu.ExpectTextFileContent("review.out", `{"verdict":"from the text"}`+"\n")
	project.dagu.ExpectTextFileContent("show.out", "calls=[]\n")
}

// The raw stream goes to a file next to the stdout log, with the suffix
// .stream.jsonl.
func TestClaudeStreamFile(t *testing.T) {
	t.Parallel()

	project := newProject(t, nil)
	project.start("ok", "cli_ok.yaml").ExpectExitCode(0)

	log := project.savedNode("review").Stdout
	require.NotEmpty(t, log)
	data, err := os.ReadFile(filepath.Clean(log + ir.AgentStreamSuffix))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 4)
	require.Contains(t, lines[0], `"subtype":"init"`)
	require.Contains(t, lines[3], `"type":"result"`)
}

// An unusable typed result fails the attempt with the kind error and a reason.
// The step publishes no output.
func TestClaudeTypedResultFailures(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		scenario string
		reason   string
	}{
		{"duplicate_field", "invalid_json"},
		{"unknown_field", "schema_mismatch"},
		{"no_structured_output", "no_result"},
		{"max_turns", "max_tool_iterations"},
		{"no_result", "no_result"},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			t.Parallel()

			project := newProject(t, nil)
			result := project.start(tc.scenario, "cli_result.yaml")
			result.ExpectNonZeroExitCode()

			require.Contains(t, summary(result), tc.reason)
			project.requireNoStdout("review.out")
			project.dagu.ExpectFileContains("review.err", "agent: attempt ended error")
		})
	}
}

// auth_required gives the exit code 77, and rate_limited gives 75. The step
// writes one line for each failed attempt. continue_on selects the code.
func TestClaudeFailureKindsAndExitCodes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		scenario string
		fixture  string
		stderr   string
	}{
		{"auth_required", "auth", "cli_auth_required.yaml", "agent: attempt ended auth_required\n"},
		{"rate_limited", "rate_limit", "cli_rate_limited.yaml", "agent: attempt ended rate_limited\n"},
		{"error keeps the exit code", "no_result", "cli_error.yaml", "agent: attempt ended error\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			project := newProject(t, nil)
			requirePartialSuccess(t, project.start(tc.scenario, tc.fixture))

			project.dagu.ExpectTextFileContent("after.out", "continued\n")
			project.dagu.ExpectFileContent("review.err", tc.stderr)
		})
	}
}

// retry_policy never retries auth_required, because a new run with the same
// credential cannot succeed.
func TestRetryPolicyDoesNotRetryAuthRequired(t *testing.T) {
	t.Parallel()

	project := newProject(t, nil)
	project.start("auth", "cli_auth_retry.yaml").ExpectNonZeroExitCode()
	require.Equal(t, 1, project.calls())
}

// retry_policy retries rate_limited as for any step, and its exit_code can
// select the code 75.
func TestRetryPolicyRetriesRateLimited(t *testing.T) {
	t.Parallel()

	project := newProject(t, nil)
	project.start("rate_limit", "cli_rate_retry.yaml").ExpectNonZeroExitCode()
	require.Equal(t, 2, project.calls())
}

// A rate limit does not end a step that has a retry_policy with exit_code [75].
// The step waits for interval_sec and makes a new attempt with the same command.
func TestRetryPolicyWaitsForARateLimit(t *testing.T) {
	t.Parallel()

	project := newProject(t, nil)
	project.start("rate_limit_then_ok", "cli_rate_retry_ok.yaml").ExpectExitCode(0)

	require.Equal(t, 2, project.calls())
	project.dagu.ExpectTextFileContent("review.out", "done after the limit\n")
	project.dagu.ExpectFileContains("review.err", "agent: attempt ended rate_limited")
	first, second := project.argv(1), project.argv(2)
	require.Equal(t, first[:len(first)-1], second[:len(second)-1], "the same command, but for the path of the MCP config")
	require.NotEqual(t, flagValue(t, first, "--mcp-config"), flagValue(t, second, "--mcp-config"), "each attempt has its own MCP config")
}

// A CLI provider other than claude runs with the model and the prompt only: none of
// the flags of claude reaches it.
func TestOtherCLIProviderGetsTheModelAndThePromptOnly(t *testing.T) {
	t.Parallel()

	project := newProject(t, nil)
	project.start("quiet", "cli_other_provider.yaml").ExpectExitCode(0)

	project.dagu.ExpectTextFileContent("review.out", "codex says hi\n")
	argv := project.readArgv("codex.argv")
	require.Equal(t, "exec", argv[0])
	require.Equal(t, "Say hello.", argv[1])
	require.Equal(t, "gpt-x", flagValue(t, argv, "--model"))
	for _, flag := range []string{"--permission-mode", "--append-system-prompt", "--tools", "--mcp-config", "--output-format"} {
		require.NotContains(t, argv, flag)
	}
	require.Equal(t, 0, project.calls(), "the fake claude did not run")
}

// readFileOf returns the content of a file of the project.
func readFileOf(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- the path is in the temp project of the test.
	require.NoError(t, err)
	return string(data)
}

// A skill of an agent.run step is a file of the agent directory of the attempt:
// Dagu passes the directory with --add-dir, adds the tool Skill to --tools and to
// --allowedTools, and deletes the directory at the end of the attempt. The saved
// node records the skills of the step.
func TestClaudeSkills(t *testing.T) {
	t.Parallel()

	project := newProject(t, nil)
	project.start("quiet", "skills_cli.yaml").ExpectExitCode(0)

	argv := project.argv(1)
	addDir := flagValue(t, argv, "--add-dir")
	require.Equal(t, "Skill", flagValue(t, argv, "--tools"))
	require.Contains(t, argv, "--allowedTools")

	for name, text := range map[string]string{
		"tone":          "Be warm and short.",
		"refund-policy": "",
		"gold":          "",
	} {
		file := filepath.Join(project.fake, "adddir.1", ".claude", "skills", name, "SKILL.md")
		data, err := os.ReadFile(file)
		require.NoError(t, err, "the skill %s is mounted", name)
		require.Contains(t, string(data), "name: "+name)
		require.Contains(t, string(data), text)
	}
	_, err := os.Stat(addDir)
	require.True(t, os.IsNotExist(err), "the agent directory is deleted after the attempt")

	var names []string
	for _, definition := range project.savedNode("review").SkillDefinitions {
		names = append(names, definition.Name)
	}
	require.Equal(t, []string{"tone", "refund-policy", "gold"}, names)
}
