// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec013_step_run_test

import (
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// TestArrayFormMalformedEntriesAreRejected proves the "Field Shape" rules for
// array-form `run`: a mapping item with more than one key, and a single-key
// mapping item whose value is itself a nested mapping or a nested array,
// must all fail validation rather than being silently coerced.
func TestArrayFormMalformedEntriesAreRejected(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		file string
	}{
		{name: "multi-key mapping item", file: "array_form_multi_key_mapping_invalid.yaml"},
		{name: "nested mapping value", file: "array_form_nested_mapping_value_invalid.yaml"},
		{name: "nested array item", file: "array_form_nested_array_item_invalid.yaml"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains("command")
		})
	}
}

// TestArrayFormSingleKeyScalarMappingIsAccepted proves the companion positive
// rule: a single-key mapping item whose value is a primitive scalar is valid
// and converts to a "key: value" command string, run in order alongside
// plain string entries.
func TestArrayFormSingleKeyScalarMappingIsAccepted(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "array_form_single_key_scalar_valid.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("out.txt", "first\nhello: world\n")
}

// TestShellSelectionPrecedence proves the full selection order from "Shell
// Selection": step with.shell beats root shell, root shell beats
// DAGU_DEFAULT_SHELL, and DAGU_DEFAULT_SHELL beats the platform discovery
// fallback (here poisoned via $SHELL). Each fixture poisons every lower-
// precedence source with a nonexistent path, so the run only succeeds if the
// higher-precedence source was actually the one selected.
func TestShellSelectionPrecedence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixtures assume a POSIX sh is available")
	}
	t.Parallel()

	t.Run("step with.shell overrides root shell and DAGU_DEFAULT_SHELL", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(
			[]string{"DAGU_DEFAULT_SHELL=/nonexistent/poison-default-shell"},
			"start", "shell_step_overrides_root_default.yaml",
		)
		result.ExpectExitCode(0)
		dagu.ExpectFileContent("result.out", "ok\n")
	})

	t.Run("root shell overrides DAGU_DEFAULT_SHELL", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(
			[]string{"DAGU_DEFAULT_SHELL=/nonexistent/poison-default-shell"},
			"start", "shell_selection_root_overrides_default.yaml",
		)
		result.ExpectExitCode(0)
		dagu.ExpectFileContent("result.out", "ok\n")
	})

	t.Run("DAGU_DEFAULT_SHELL overrides the platform fallback", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(
			[]string{"SHELL=/nonexistent/poison-shell-env", "DAGU_DEFAULT_SHELL=sh"},
			"start", "shell_default_overrides_platform.yaml",
		)
		result.ExpectExitCode(0)
		dagu.ExpectFileContent("result.out", "ok\n")
	})
}

// TestStdinPipesFileToStepProcess proves the "Standard Input" source rules: the
// named file reaches the step process standard input, a step output file path
// is an accepted source, and every array-form entry reads the file from its
// start.
func TestStdinPipesFileToStepProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixtures use cat to read standard input")
	}
	t.Parallel()

	cases := []struct {
		name    string
		file    string
		content string
	}{
		{name: "single command", file: "stdin_file_piped_valid.yaml", content: "payload\n"},
		{name: "array form", file: "stdin_array_run_valid.yaml", content: "payload\npayload\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			dagu.WriteFile("payload.txt", "payload\n")
			result := dagu.Run("start", tc.file)
			result.ExpectExitCode(0)
			dagu.ExpectFileContent("out.txt", tc.content)
		})
	}

	t.Run("step output file", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "stdin_step_output_file_valid.yaml")
		result.ExpectExitCode(0)
		dagu.ExpectFileContent("out.txt", "payload\n")
	})
}

// TestStdinErrors proves the "Standard Input" error rules: an action that
// cannot consume standard input rejects the field at validation, and an
// unreadable file fails the step at runtime.
func TestStdinErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixtures use cat to read standard input")
	}
	t.Parallel()

	t.Run("unsupported action is rejected by validate", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "stdin_unsupported_action_invalid.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("stdin")
	})

	runtimeFailures := []struct {
		name string
		file string
	}{
		{name: "missing file", file: "stdin_missing_file_invalid.yaml"},
		{name: "unresolved reference", file: "stdin_unresolved_reference_invalid.yaml"},
		{name: "reference resolving to nothing", file: "stdin_empty_reference_invalid.yaml"},
	}

	for _, tc := range runtimeFailures {
		t.Run(tc.name+" fails the step", func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("start", tc.file)
			result.ExpectNonZeroExitCode()
		})
	}
}
