// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec040_router_route holds black-box conformance tests for
// Spec 040: Router Route Action.
package spec040_router_route_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

type routeFile struct {
	path    string
	content string
}

// Route matching controls target execution independently for each pattern.
func TestRouteRuntime(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		file   string
		want   []routeFile
		absent []string
	}{
		{
			name:   "exact match rejects a substring",
			file:   "basic_route.yaml",
			want:   []routeFile{{"b.out", "ran-b\n"}, {"route.txt", "Router evaluating: ab\n  a -> [branch_a]\n  ab -> [branch_b]\n"}},
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
			name: "unresolved literal matches a catch-all",
			file: "unresolved_value.yaml",
			want: []routeFile{{"matched.out", "matched\n"}, {"route.txt", "Router evaluating: $DAGU_CONFORMANCE_UNDEFINED_ROUTE\n  re:.* -> [matched]\n"}},
		},
		{
			name:   "no matching pattern skips every target and still succeeds",
			file:   "no_route_matches.yaml",
			absent: []string{"a.out"},
		},
		{
			name:   "num: pattern compares the value as a number",
			file:   "numeric_route.yaml",
			want:   []routeFile{{"auto_approve.out", "approve\n"}, {"route.txt", "Router evaluating: 0.95\n  num:<0.9 -> [human_review]\n  num:>=0.9 -> [auto_approve]\n"}},
			absent: []string{"human_review.out"},
		},
		{
			// The diagnostic prints the route as authored, not as resolved: a
			// threshold may come from a secret.
			name:   "a num: route threshold can be a value reference",
			file:   "numeric_route_threshold_reference.yaml",
			want:   []routeFile{{"auto_approve.out", "approve\n"}, {"route.txt", "Router evaluating: 0.95\n  num:<${threshold} -> [human_review]\n  num:>=${threshold} -> [auto_approve]\n"}},
			absent: []string{"human_review.out"},
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
			name:        "empty routes",
			file:        "empty_routes.yaml",
			stderrParts: []string{"requires at least one route"},
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
		{
			name:        "route pattern with an unsupported numeric operator",
			file:        "invalid_numeric_route.yaml",
			stderrParts: []string{"numeric comparison is invalid"},
		},
		{
			name:        "route pattern with an uncompilable regexp",
			file:        "invalid_regex_route.yaml",
			stderrParts: []string{"regexp is invalid"},
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

// A num: route is the one break from the router's leniency toward values that
// resolve to literal text. The failure lands on the router itself, and the
// diagnostic is still written so the routing decision is reportable.
func TestNumericRouteRejectsNonNumericValue(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "numeric_route_not_a_number.yaml")
	result.ExpectNonZeroExitCode()
	dagu.ExpectNoFile("auto_approve.out")
	dagu.ExpectFileContent("route.txt", "Router evaluating: $DAGU_CONFORMANCE_UNDEFINED_ROUTE\n  num:>=0.9 -> [auto_approve]\n")
}

// An undecidable routing decision is not partially carried out: a route that
// matches the value exactly still does not run its target.
func TestNumericRouteFailureBlocksMatchingRoutes(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "numeric_route_mixed_patterns.yaml")
	result.ExpectNonZeroExitCode()
	dagu.ExpectNoFile("auto_approve.out")
	dagu.ExpectNoFile("exact_match.out")
}
