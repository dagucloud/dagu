// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec048_duckdb holds black-box conformance tests for
// Spec 048: DuckDB Action.
package spec048_duckdb_test

import (
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// TestActionOfficialDuckDBLive proves the action executor's real-world
// path end to end: resolving the official dagucloud/duckdb@v1 action over
// a real git clone, letting the action DAG provision the duckdb tool via
// its own tools: declaration, running it, and validating/returning its
// output. This is the one test in this package that needs network access
// (to clone the action and download the duckdb binary), so it is skipped
// when github.com is unreachable, and it is deliberately not parallel: it
// is already the slowest test here (an uncached run downloads a real
// tool), and does not benefit from further contention.
func TestActionOfficialDuckDBLive(t *testing.T) {
	requireGitHubAccess(t)

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "duckdb_live.yaml")
	result.ExpectExitCode(0)

	// The action's own output is the manifest-validated
	// {"result": "<raw duckdb -json output>"}. Parsing both layers and
	// asserting on the query's actual result values (rather than the whole
	// string verbatim) keeps this test from being brittle to
	// formatting/whitespace differences across duckdb versions.
	var actionOutput struct {
		Result string `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout())), &actionOutput))

	var rows []struct {
		Answer int    `json:"answer"`
		Engine string `json:"engine"`
	}
	require.NoError(t, json.Unmarshal([]byte(actionOutput.Result), &rows))
	require.Equal(t, []struct {
		Answer int    `json:"answer"`
		Engine string `json:"engine"`
	}{{Answer: 42, Engine: "duckdb"}}, rows)
}

func TestActionSourceLocal(t *testing.T) {
	t.Run("resolves a local source: action and validates its input and output against the manifest", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "local_action_success.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, `{"echoed":"hello-from-local-action"}`+"\n", stepStdout(t, result.Stdout()))
	})

	t.Run("input missing a required field is rejected against the manifest's inputs schema", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "local_action_missing_input.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("action input does not match inputs schema")
		result.ExpectStderrContains(`missing properties: ["message"]`)
	})

	t.Run("input with an unexpected field is rejected against the manifest's inputs schema", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "local_action_extra_input.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("action input does not match inputs schema")
		result.ExpectStderrContains(`unexpected additional properties ["unexpected_field"]`)
	})

	t.Run("an action DAG that sets working_dir itself is rejected", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "local_action_bad_workdir.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("must not set working_dir")
	})

	t.Run("a source bundle with no manifest file fails with a clear error", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "local_action_no_manifest.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("read action manifest")
	})

	t.Run("an action DAG's output is rejected against the manifest's outputs schema", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "local_action_bad_output.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("action output does not match outputs schema")
		result.ExpectStderrContains(`unexpected additional properties ["wrong_field"]`)
	})

	t.Run("a source: target that resolves to neither a local directory nor a git remote fails", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "local_action_nonexistent.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("clone action source")
	})
}

// TestActionRefValidation proves the action executor's build-time
// reference-syntax validation: it does not need a resolvable bundle,
// since it rejects malformed references before ever trying to fetch one.
func TestActionRefValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		file   string
		errStr string
	}{
		{
			name:   "a bare name with no @version and no known meaning is unknown",
			file:   "unknown_action.yaml",
			errStr: `unknown action "notarealaction"`,
		},
		{
			name:   "a versioned reference that is neither an official nor a GitHub action is rejected",
			file:   "malformed_versioned_ref.yaml",
			errStr: "versioned action references must use official action@version or GitHub owner/repo@version",
		},
		{
			name:   "a source: reference missing @version is rejected",
			file:   "source_missing_version.yaml",
			errStr: "source action references must use source:target@version",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.errStr)
		})
	}
}
