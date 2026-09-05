// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec054_template holds black-box conformance tests for
// Spec 054: Template Action.
package spec054_template_test

import (
	"os"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestTemplateLive(t *testing.T) {
	t.Run("renders with.data into with.template and writes the result to stdout", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "basic.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "Hello, World!", lastStepStdout(t, result.Stdout()))
	})

	t.Run("with.output writes the rendered result to a file, creating missing parent directories", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "output_file.yaml")
		result.ExpectExitCode(0)

		data, err := os.ReadFile(dagu.ProjectPath("nested/dir/rendered.txt")) // #nosec G304 -- fixed test path.
		require.NoError(t, err)
		require.Equal(t, "Hello, File!", string(data))
	})

	t.Run("with.template_ref resolves an exact scoped reference to the template text", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "template_ref_env.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "Hi Ref", lastStepStdout(t, result.Stdout()))
	})

	// with.template's text is handed to Go's text/template engine as-is,
	// never through Dagu's own ${...} substitution -- unlike an ordinary
	// run: script body, which does substitute ${...}. with.data's values,
	// by contrast, go through the same resolution any other with: field
	// gets, before being handed to the template as pipeline data.
	t.Run("the template body is not pre-resolved by Dagu, but with.data values are", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "no_double_resolution.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "dagu-literal: ${FOO}, go-template: bar", lastStepStdout(t, result.Stdout()))
	})

	t.Run("template functions include the hermetic sprig set plus Dagu's pipeline-friendly overrides", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "functions.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, `BOB | a,b,c | "bob"`, lastStepStdout(t, result.Stdout()))
	})

	t.Run("env, a non-hermetic sprig function, is blocked", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "blocked_env_func.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains(`function "env" not defined`)
	})

	t.Run("now, a non-deterministic sprig function, is blocked", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "blocked_now_func.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains(`function "now" not defined`)
	})

	t.Run("a template referencing an undeclared data key fails at run time", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "missingkey.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("map has no entry for key")
	})

	t.Run("a malformed template fails to parse at run time", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "parse_error.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("template: parse error")
	})
}

// TestTemplateValidation proves that with.template/with.template_ref
// exclusivity and with.template_ref's exact-reference-format requirement
// are enforced at DAG-build time, ahead of any step running.
func TestTemplateValidation(t *testing.T) {
	t.Parallel()

	t.Run("neither with.template nor with.template_ref fails validate", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "missing_both.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("requires exactly one of with.template or with.template_ref")
	})

	t.Run("both with.template and with.template_ref fails validate", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "both_set.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("requires exactly one of with.template or with.template_ref")
	})

	t.Run("a with.template_ref that is not one complete scoped reference fails validate", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "bad_template_ref.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("must be one complete scoped value reference")
	})
}
