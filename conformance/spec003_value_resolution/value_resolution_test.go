// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec003_value_resolution_test

import (
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// TestEscapeParityAcrossNamespaces proves the odd/even backslash-run escape
// rule from the "Escaped Dagu References" section applies identically to
// every Dagu-owned reference namespace, not just one of them:
//
//   - 0 backslashes: unescaped, the reference resolves normally.
//   - 1 backslash (odd): the reference is escaped; it stays literal, with the
//     escape marker itself removed.
//   - 2 backslashes (even): unescaped again; the reference resolves and the
//     backslash pair is preserved untouched ahead of the resolved value.
//   - 3 backslashes (odd): escaped again; one marker is removed, leaving the
//     remaining backslash pair ahead of the still-literal reference text.
func TestEscapeParityAcrossNamespaces(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		file     string
		refText  string
		resolved string
	}{
		{name: "consts", file: "escape_matrix_consts.yaml", refText: "${consts.name}", resolved: "prod"},
		{name: "params", file: "escape_matrix_params.yaml", refText: "${params.name}", resolved: "prod"},
		{name: "env", file: "escape_matrix_env.yaml", refText: "${env.NAME}", resolved: "prod"},
		{name: "steps", file: "escape_matrix_steps.yaml", refText: "${steps.build.outputs.image}", resolved: "v1.2.3"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("start", tc.file)
			result.ExpectExitCode(0)

			expected := tc.resolved + "\n" + // 0 backslashes: resolved
				tc.refText + "\n" + // 1 backslash: escaped, marker removed, stays literal
				`\\` + tc.resolved + "\n" + // 2 backslashes: unescaped, pair preserved, resolves
				`\\` + tc.refText + "\n" // 3 backslashes: escaped, one pair remains, stays literal
			dagu.ExpectFileContent("matrix.txt", expected)
		})
	}
}

// TestStringInsertionCoercion proves the "String Insertion" rules: booleans
// insert as "true"/"false", integers insert in base-10 decimal, and
// non-integer numbers insert using the shortest round-trippable decimal
// representation rather than the author's original literal text.
func TestStringInsertionCoercion(t *testing.T) {
	t.Parallel()

	t.Run("boolean and integer", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "insertion_boolean_and_integer.yaml")
		result.ExpectExitCode(0)
		dagu.ExpectFileContent("insertion.txt", "true\nfalse\n-7\n")
	})

	t.Run("number strips trailing zeros to shortest round-trippable form", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "insertion_number_trailing_zeros.yaml")
		result.ExpectExitCode(0)
		// Authored as 3.140000 and 2.0; the spec requires the shortest
		// round-trippable text, not the author's original digit run.
		dagu.ExpectFileContent("insertion.txt", "3.14\n2\n")
	})

	t.Run("number normalizes exponent notation to plain decimal", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "insertion_number_exponent_notation.yaml")
		result.ExpectExitCode(0)
		// Authored as 1.5e2; inserted text must be plain decimal, not the
		// author's exponent-notation literal.
		dagu.ExpectFileContent("insertion.txt", "150\n")
	})
}

func TestInsertedTextStaysLiteral(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "--run-id=spec003-insertion", "insertion_literal_text.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("insertion.txt", "p\\$INSERTED\n$INSERTED/data\n$INSERTED/data\nexpanded\nexpanded$INSERTED/data\n"+
		"$INSERTED/data\n$INSERTED\np\\$INSERTED\n$INSERTED/data-spec003-insertion\n")
}

// A retry rebuilds the DAG from its stored definition, and root env inserted
// at the first load must stay literal on that path too.
func TestRootEnvInsertionOnRetry(t *testing.T) {
	t.Parallel()

	const runID = "spec003-root-env-retry"
	dagu := harness.NewRunner(t)
	env := []string{"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu")}

	dagu.RunWithEnv(env, "start", "--run-id="+runID, "root_env_retry.yaml").ExpectNonZeroExitCode()
	dagu.WriteFile("ready", "")
	dagu.RunWithEnv(env, "retry", "--run-id="+runID, "root_env_retry.yaml").ExpectExitCode(0)
	dagu.ExpectTextFileContent("root_copy.out", "$INSERTED/data\n$INSERTED/data\n")
}

// TestDefectAndRuntimeOnlyNoticeClassification proves the two notice classes
// from "Unresolved Supported References" are actually distinguished, not just
// both labeled generically:
//
//   - A defect (a reference the spec statically cannot resolve, such as an
//     undeclared const name) must be reported by `dagu validate` even without
//     `--show-unresolved`.
//   - A runtime-only notice (a well-formed reference whose availability
//     depends on a value only the caller can supply at run start, such as a
//     required param not yet provided at validate time) must stay out of
//     default `dagu validate` output and appear only under
//     `--show-unresolved`.
//
// Neither class is a validation error: every case here still exits zero.
func TestDefectAndRuntimeOnlyNoticeClassification(t *testing.T) {
	t.Parallel()

	t.Run("defect: unknown const is reported without --show-unresolved", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "notice_defect_unknown_const.yaml")
		result.ExpectExitCode(0)
		result.ExpectStderrContains("${consts.unknown_name}", "steps[0].run")
	})

	t.Run("defect: unknown const in root ssh is reported without --show-unresolved", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "notice_defect_unknown_const_ssh.yaml")
		result.ExpectExitCode(0)
		result.ExpectStderrContains("${consts.unknown_host}", "ssh.host")
	})

	t.Run("step-output reference without depends is an inferred dependency, not a notice", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "inferred_dependency_env.yaml")
		result.ExpectExitCode(0)
		result.ExpectStderrContains("inferred: build -> deploy (steps[1].env[0])")
		result.ExpectStderrNotContains("${steps.build.outputs.image}")
	})

	t.Run("runtime-only: missing required param stays quiet by default", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "notice_runtime_only_missing_param.yaml")
		result.ExpectExitCode(0)
		result.ExpectStderr("")
	})

	t.Run("runtime-only: missing required param is shown under --show-unresolved", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "--show-unresolved", "notice_runtime_only_missing_param.yaml")
		result.ExpectExitCode(0)
		result.ExpectStderrContains("${params.environment}")
	})
}
