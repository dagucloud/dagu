// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec067_harness tests the harness executor through the Dagu binary.
package spec067_harness_test

import (
	"fmt"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestHarnessLive(t *testing.T) {
	t.Parallel()

	t.Run("implicit working directory remains runnable", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		writeFakeHarnessScripts(dagu)
		dagu.WriteFile("implicit.yaml", fmt.Sprintf(`harnesses:
  fake:
    binary: sh
    prefix_args: [%q]
steps:
  - id: review
    action: harness.run
    with:
      provider: fake
      prompt: Review this repository.
`, dagu.ProjectPath("scripts/fake_ok.sh")))
		result := dagu.Run("start", "implicit.yaml")
		result.ExpectExitCode(0)
		require.Contains(t, stepStdout(t, result.Stdout(), 0), "ARG:Review this repository.")
	})

	t.Run("invocation and fallback", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		env := writeFakeHarnessScripts(dagu)
		result := dagu.RunWithEnv(env, "start", "happy_path.yaml")
		result.ExpectExitCode(0)

		// arg_mode (prompt_mode: arg, default prompt_position: before_flags):
		// the prompt comes first, then flags sorted by key -- "label" (mapped
		// to --tag via option_flags) before "verbose" (a bare flag, since
		// true).
		argMode := stepStdout(t, result.Stdout(), 0)
		requireLines(t, argMode,
			"ARG:hello from arg mode",
			"ARG:--tag",
			"ARG:build",
			"ARG:--verbose",
			"STDIN:",
		)

		// flag_mode (prompt_mode: flag, prompt_position: after_flags):
		// flags first, then --prompt <value>; with.stdin is piped to the
		// process's stdin.
		flagMode := stepStdout(t, result.Stdout(), 1)
		requireLines(t, flagMode,
			"ARG:--priority",
			"ARG:5",
			"ARG:--prompt",
			"ARG:flag prompt text",
			"STDIN:piped script text",
		)

		// stdin_mode (prompt_mode: stdin): no argv at all; the prompt and
		// with.stdin are joined with a blank line and both piped to stdin.
		stdinMode := stepStdout(t, result.Stdout(), 2)
		requireLines(t, stdinMode,
			"STDIN:stdin-mode prompt",
			"",
			"stdin-mode script",
		)

		// dash_mode (flag_style: single_dash): a custom flag becomes -count
		// 3, not --count 3.
		dashMode := stepStdout(t, result.Stdout(), 3)
		requireLines(t, dashMode,
			"ARG:dash prompt",
			"ARG:-count",
			"ARG:3",
			"STDIN:",
		)

		// fallback_success: the primary provider (fake_fail.sh) fails, so
		// the step retries with.fallback[0] (fake_backup, fake_ok.sh) and
		// the step as a whole succeeds. The primary's own stderr and the
		// wrapper's own "trying fallback" message both land in the step's
		// stderr; the final stdout is the fallback provider's own output.
		fallbackStdout := stepStdout(t, result.Stdout(), 4)
		requireLines(t, fallbackStdout, "ARG:trying fallback", "STDIN:")
		fallbackStderr := stepStderr(t, result.Stdout(), 0) // the only step in this fixture that writes any stderr
		requireContainsAll(t, fallbackStderr,
			"fake_fail: simulated failure",
			"harness: attempt 1/2 with fake_primary failed; trying fallback 2/2 with fake_backup",
		)
	})

	t.Run("process and binary errors", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		env := writeFakeHarnessScripts(dagu)
		result := dagu.RunWithEnv(env, "start", "error_scenarios.yaml")
		result.ExpectNonZeroExitCode()

		// no_fallback_fail: fake_fail.sh's own exit code (7) becomes the
		// step's exit code, and its stderr is captured as usual -- this is
		// a real process that ran and failed, not a wrapper validation
		// error.
		require.Contains(t, stepStderr(t, result.Stdout(), 0), "fake_fail: simulated failure")
		require.Contains(t, result.Stdout(), "exit status 7")

		// missing_binary: the configured binary path does not exist, so the
		// step fails before any process starts, with a distinct
		// "harness: failed to resolve binary" error (no stdout/stderr log
		// of a process that never ran).
		require.Contains(t, result.Stdout(), "harness: failed to resolve binary")
		require.Contains(t, result.Stdout(), "does_not_exist.sh")
	})

	t.Run("declared output", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		env := writeFakeHarnessScripts(dagu)
		result := dagu.RunWithEnv(env, "start", "downstream_reference.yaml")
		result.ExpectExitCode(0)

		require.Equal(t, "named=ARG:hello\nSTDIN:\n", stepStdout(t, result.Stdout(), 1),
			"output: NAME trims one trailing newline from the captured stdout")
	})
}

// TestHarnessValidation covers dagu validate for the harness executor.
// Unlike the remote actions in specs 060-066, harness is a built-in
// executor: dagu validate resolves its provider configuration (a known
// built-in provider name, a defined custom harnesses entry, or an error) and
// its harnesses: definitions fully, without any network access, since both
// live in this binary and the DAG file itself. The one thing it does NOT
// check is whether the configured binary actually exists -- that is a
// runtime-only concern (see TestHarnessLive's missing_binary case).
func TestHarnessValidation(t *testing.T) {
	t.Parallel()

	t.Run("implicit working directory warns without failing", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		definition := `steps:
  - id: review
    action: harness.run
    with:
      provider: claude
      prompt: Review this repository.
`
		dagu.WriteFile("implicit.yaml", definition)
		result := dagu.Run("validate", "implicit.yaml")
		result.ExpectExitCode(0)
		result.ExpectStderrContains("has no explicit working_dir")
		dagu.WriteFile("configured.yaml", "working_dir: .\n"+definition)
		result = dagu.Run("validate", "configured.yaml")
		result.ExpectExitCode(0)
		require.NotContains(t, result.Stderr(), "has no explicit working_dir")
	})

	t.Run("host directory does not suppress container warning", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		dagu.WriteFile("container.yaml", `working_dir: .
steps:
  - id: review
    action: harness.run
    container: {image: alpine}
    with:
      provider: claude
      prompt: Review this repository.
`)
		result := dagu.Run("validate", "container.yaml")
		result.ExpectExitCode(0)
		result.ExpectStderrContains("has no explicit container.working_dir")
	})

	t.Run("a well-formed harness step and its custom harnesses: definitions pass validate", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "happy_path.yaml")
		result.ExpectExitCode(0)
	})

	invalidCases := []struct {
		name       string
		file       string
		wantStderr string
	}{
		{
			name:       "with.prompt missing",
			file:       "invalid_missing_prompt.yaml",
			wantStderr: "with.prompt is required",
		},
		{
			name:       "with.provider names neither a built-in nor a defined custom harness",
			file:       "invalid_unknown_provider.yaml",
			wantStderr: `unknown provider "totally-bogus-provider"`,
		},
		{
			name:       "with.binary is rejected in favor of a named harnesses entry",
			file:       "invalid_binary_in_with.yaml",
			wantStderr: "config.binary is not supported",
		},
		{
			name:       "with.prompt_args is rejected in favor of a named harnesses entry",
			file:       "invalid_prompt_args_in_with.yaml",
			wantStderr: "config.prompt_args is not supported",
		},
		{
			name:       "a harnesses: definition missing binary",
			file:       "invalid_harness_def_missing_binary.yaml",
			wantStderr: "binary is required",
		},
		{
			name:       "a harnesses: definition with an unsupported prompt_mode",
			file:       "invalid_harness_def_bad_prompt_mode.yaml",
			wantStderr: "prompt_mode must be one of: arg, flag, stdin",
		},
		{
			name:       "prompt_mode: flag without prompt_flag",
			file:       "invalid_harness_def_prompt_flag_required.yaml",
			wantStderr: "prompt_flag is required when prompt_mode is flag",
		},
		{
			name:       "prompt_flag set when prompt_mode is not flag",
			file:       "invalid_harness_def_prompt_flag_not_allowed.yaml",
			wantStderr: "prompt_flag is only valid when prompt_mode is flag",
		},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.wantStderr)
		})
	}
}
