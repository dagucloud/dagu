// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec090_agent_tools_test

import (
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// The block agent_tools of the Dagu configuration is closed and strict. An unknown
// key, a duplicate key, a value of the wrong type and a value out of range stop Dagu
// at start, with the key, the expected type and the value. No value is changed to
// make it fit.
func TestAgentToolsConfigIsClosedAndStrict(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		fixture string
		message []string
	}{
		{"config_agent_tools_unknown_key.yaml", []string{`unknown key "agent_tools.max_tool"`, "valid keys are max_tools, tool_result_max_bytes"}},
		{"config_agent_tools_wrong_type.yaml", []string{`key "agent_tools.max_tools"`, "an integer of at least 1", `got "8"`}},
		{"config_agent_tools_out_of_range.yaml", []string{`key "agent_tools.max_tools"`, "an integer of at least 1", "got 0"}},
		{"config_agent_tools_negative_bytes.yaml", []string{`key "agent_tools.tool_result_max_bytes"`, "an integer of at least 0", "got -1"}},
		{"config_agent_tools_duplicate_key.yaml", []string{"max_tools"}},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("config", "--config", dagu.ProjectPath(tc.fixture))
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.message...)
		})
	}
}

// The environment variables of the keys do not set them: the file is the only source.
func TestAgentToolsConfigHasNoEnvironmentBinding(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv([]string{"DAGU_AGENT_TOOLS_MAX_TOOLS=3", "DAGU_AGENT_TOOLS_TOOL_RESULT_MAX_BYTES=3"},
		"config", "--config", dagu.ProjectPath("config_agent_tools_ok.yaml"))
	result.ExpectExitCode(0)
	out := strings.Join(strings.Fields(result.Stdout()), " ")
	if !strings.Contains(out, "agent_tools.max_tools: 8 file") {
		t.Errorf("an environment variable changed agent_tools.max_tools\n%s", result.Stdout())
	}
}

// The command dagu config prints the value that applies for each key of the block,
// with its source: the file or the default.
func TestConfigCommandPrintsTheEffectiveAgentTools(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("config", "--config", dagu.ProjectPath("config_agent_tools_ok.yaml"))
	result.ExpectExitCode(0)
	out := strings.Join(strings.Fields(result.Stdout()), " ")
	for _, want := range []string{
		"agent_tools.max_tools: 8 file",
		"agent_tools.tool_result_max_bytes: 0 file",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dagu config does not print %q\n%s", want, result.Stdout())
		}
	}

	defaults := dagu.Run("config", "--config", dagu.ProjectPath("config_agent_tools_default.yaml"))
	defaults.ExpectExitCode(0)
	out = strings.Join(strings.Fields(defaults.Stdout()), " ")
	for _, want := range []string{
		"agent_tools.max_tools: 64 default",
		"agent_tools.tool_result_max_bytes: 524288 default",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dagu config does not print %q\n%s", want, defaults.Stdout())
		}
	}
}
