// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec040_router_route holds black-box conformance tests for
// Spec 040: Router Route Action.
package spec040_router_route_test

import (
	"os"
	"regexp"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// stdoutLogPattern matches the per-step captured-stdout log path dagu start
// prints in its tree render, e.g. "└─stdout: /path/to/step.<ts>.<run>.out".
// Captures through the line ending rather than \S+, so a project path
// containing spaces isn't truncated.
var stdoutLogPattern = regexp.MustCompile(`stdout: ([^\r\n]+)`)

// stepStdout reads the exact bytes a step wrote to stdout, by locating its
// captured-output log file from dagu start's own tree render and reading it
// directly. This is the only way to check the router step's diagnostic
// output verbatim: the tree render dagu start prints to its own stdout
// re-indents an inlined step's output with its own tree-drawing prefix, so
// checking result.Stdout() directly could not distinguish the diagnostic's
// own two-space route-line indent from the tree renderer's.
func stepStdout(t *testing.T, daguStartOutput string) string {
	t.Helper()

	match := stdoutLogPattern.FindStringSubmatch(daguStartOutput)
	require.Lenf(t, match, 2, "expected a stdout log path in output:\n%s", daguStartOutput)
	data, err := os.ReadFile(match[1]) // #nosec G304 -- path comes from the harness's own trusted output.
	require.NoError(t, err)
	return string(data)
}

type routeFile struct {
	path    string
	content string
}

// TestRouteRuntime proves router.route's runtime contract: routing is
// precondition injection per target, not first-match-wins branching, so
// multiple patterns (or multiple targets under one pattern) can all match
// and run at once, a value matching nothing skips every target without
// failing the DAG-run, and a step depending on a skipped target still runs
// (continueOn.skipped, injected on every target).
func TestRouteRuntime(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		file   string
		want   []routeFile
		absent []string
	}{
		{
			name:   "exact match runs only the matching target",
			file:   "basic_route.yaml",
			want:   []routeFile{{"b.out", "ran-b\n"}},
			absent: []string{"a.out"},
		},
		{
			// Separate output files per target: if the server_error pattern
			// (re:^5\d\d$) incorrectly also matched "404", overwriting a shared
			// file could hide that from this assertion.
			name:   "re: pattern matches as a regular expression",
			file:   "regex_route.yaml",
			want:   []routeFile{{"client_error.out", "4xx\n"}},
			absent: []string{"server_error.out"},
		},
		{
			// Not first-match-wins: both patterns match "500", so both targets run.
			name: "multiple matching patterns all run",
			file: "multiple_routes_match.yaml",
			want: []routeFile{{"server_error.out", "5xx\n"}, {"catch_all.out", "other\n"}},
		},
		{
			name: "one pattern with multiple targets fans out to all of them",
			file: "fanout_single_route.yaml",
			want: []routeFile{{"t1.out", "t1\n"}, {"t2.out", "t2\n"}},
		},
		{
			name:   "no matching pattern skips every target and still succeeds",
			file:   "no_route_matches.yaml",
			absent: []string{"a.out"},
		},
		{
			// after_a depends on branch_a, which is skipped (its route did not
			// match); continueOn.skipped on branch_a means after_a still runs.
			name:   "a step depending on a skipped target still runs",
			file:   "downstream_of_skipped.yaml",
			want:   []routeFile{{"after_a.out", "after-a\n"}, {"b.out", "ran-b\n"}},
			absent: []string{"a.out"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("start", tc.file)
			result.ExpectExitCode(0)
			for _, f := range tc.want {
				dagu.ExpectFileContent(f.path, f.content)
			}
			for _, f := range tc.absent {
				dagu.ExpectNoFile(f)
			}
		})
	}
}

// TestRouteDiagnosticOutput proves the router step's own diagnostic output
// is exactly "Router evaluating: <value>" followed by one two-space-indented
// "<pattern> -> [<targets>]" line per route, in order, as one contiguous
// block -- not just that those pieces appear somewhere in the output, which
// would also pass if the lines were reordered or interleaved with other
// content.
func TestRouteDiagnosticOutput(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "basic_route.yaml")
	result.ExpectExitCode(0)
	require.Equal(t, "Router evaluating: b\n  a -> [branch_a]\n  b -> [branch_b]\n", stepStdout(t, result.Stdout()))
}

// TestRouteValidation proves the errors DAG-build-time validation rejects
// before the DAG ever runs.
func TestRouteValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		file        string
		stderrParts []string
	}{
		{
			name:        "missing with.value",
			file:        "missing_value.yaml",
			stderrParts: []string{"with.value is required"},
		},
		{
			name:        "missing with.routes",
			file:        "missing_routes.yaml",
			stderrParts: []string{"with.routes is required"},
		},
		{
			name:        "same step targeted by more than one route",
			file:        "duplicate_target.yaml",
			stderrParts: []string{"is targeted by multiple routes"},
		},
		{
			name:        "route targets a step that does not exist",
			file:        "nonexistent_target.yaml",
			stderrParts: []string{"references non-existent step"},
		},
		{
			name:        "rejected in a type: chain DAG",
			file:        "chain_type_rejected.yaml",
			stderrParts: []string{"router steps require type 'graph'"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("start", tc.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.stderrParts...)
		})
	}
}
