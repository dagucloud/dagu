// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec015_step_run_script_test

import (
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/conformance/harness"
)

func TestScriptFormRuntimeUnix(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fixtures use POSIX shell snippets")
	}

	cases := []struct {
		name    string
		file    string
		output  string
		content string
		env     []string
	}{
		{
			name:    "multiline shell state remains in one script",
			file:    "multiline_state.yaml",
			output:  "multiline-state.txt",
			content: "prod\n",
		},
		{
			name:    "shell operators remain inside the script",
			file:    "operators_preserved.yaml",
			output:  "operators-preserved.txt",
			content: "beta\n",
		},
		{
			name:    "leading blank line does not hide shebang position",
			file:    "leading_blank_shebang_ignored.yaml",
			output:  "leading-blank-shebang.txt",
			content: "not-a-shebang\n",
		},
		{
			name:    "root shell does not suppress shebang",
			file:    "shebang_root_shell_bypass.yaml",
			output:  "root-shell-bypass.txt",
			content: "shebang-won\n",
		},
		{
			name:    "runtime default shell does not suppress shebang",
			file:    "shebang_runtime_default_bypass.yaml",
			output:  "runtime-default-bypass.txt",
			content: "shebang-won\n",
			env:     []string{"DAGU_DEFAULT_SHELL=sh -c"},
		},
		{
			name:    "explicit step shell suppresses direct shebang invocation",
			file:    "step_shell_suppresses_shebang.yaml",
			output:  "step-shell-suppresses-shebang.txt",
			content: "step-shell-won\n",
		},
		{
			name:    "background work must be waited by script",
			file:    "background_wait.yaml",
			output:  "background-wait.txt",
			content: "done\n",
		},
		{
			name:    "script outputs collect after success",
			file:    "valid_outputs.yaml",
			output:  "outputs-valid.txt",
			content: "from-script\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			var result *harness.Result
			if len(tc.env) > 0 {
				result = dagu.RunWithEnv(tc.env, "start", tc.file)
			} else {
				result = dagu.Run("start", tc.file)
			}
			result.ExpectExitCode(0)
			dagu.ExpectFileContent(tc.output, tc.content)
		})
	}
}

func TestScriptFormFailuresUnix(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fixtures use POSIX shell snippets")
	}

	t.Run("default shell fail fast", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "default_fail_fast.yaml")
		result.ExpectExitCode(1)
		dagu.ExpectFileContent("default-fail-fast.txt", "before\n")
	})

	t.Run("unix command carrier fails before script body", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "unix_reject_c_carrier.yaml")
		result.ExpectExitCode(1)
		result.ExpectStderrContains("script form", "-c")
		dagu.ExpectNoFile("carrier-ran.txt")
	})

	t.Run("invalid outputs fail after script success", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "invalid_outputs.yaml")
		result.ExpectExitCode(1)
		result.ExpectStderrContains("undeclared_value", "undeclared step output")
		dagu.ExpectNoFile("invalid-output-downstream.txt")
	})
}

func TestScriptDiagnosticsDoNotDumpResolvedScript(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX shell snippets")
	}

	const runID = "spec015-diagnostics"
	const file = "diagnostics_do_not_dump_script.yaml"

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "--run-id", runID, file)
	result.ExpectExitCode(1)
	result.ExpectStderrContains("nonexistent_command_015")
	result.ExpectStderrNotContains(
		"SPEC015_SCRIPT_DUMP_SENTINEL",
		"printf 'SPEC015_SCRIPT_DUMP_SENTINEL",
		"--- script content ---",
		"dagu_script-",
	)

	status := dagu.Run("status", "--run-id", runID, file)
	status.ExpectStdoutNotContains(
		"SPEC015_SCRIPT_DUMP_SENTINEL",
		"printf 'SPEC015_SCRIPT_DUMP_SENTINEL",
		"--- script content ---",
		"dagu_script-",
	)
	status.ExpectStderrNotContains(
		"SPEC015_SCRIPT_DUMP_SENTINEL",
		"printf 'SPEC015_SCRIPT_DUMP_SENTINEL",
		"--- script content ---",
		"dagu_script-",
	)

	history := dagu.Run("history", "--run-id", runID, "--format", "json")
	history.ExpectStdoutNotContains(
		"SPEC015_SCRIPT_DUMP_SENTINEL",
		"printf 'SPEC015_SCRIPT_DUMP_SENTINEL",
		"--- script content ---",
		"dagu_script-",
	)
	history.ExpectStderrNotContains(
		"SPEC015_SCRIPT_DUMP_SENTINEL",
		"printf 'SPEC015_SCRIPT_DUMP_SENTINEL",
		"--- script content ---",
		"dagu_script-",
	)
}
