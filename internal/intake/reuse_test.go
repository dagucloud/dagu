// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake_test

import (
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/collections"
	"github.com/dagucloud/dagu/v2/internal/intake"
	"github.com/dagucloud/dagu/v2/internal/ir"
	_ "github.com/dagucloud/dagu/v2/internal/runtime/builtin"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/require"
)

// reuseSpec reads a page, keeps the rows a filter passes, and reports them;
// the filter reads the page's output by reference, so it depends on it
// without depends.
const reuseSpec = `name: listings
params:
  - MAX: 7000
env:
  - SITE: https://homes.example.com
steps:
  - id: page
    run: echo rows
    output: ROWS
  - id: filter
    run: echo "${steps.page.outputs.ROWS} under 7000"
    output: KEPT
  - id: report
    run: echo report
    depends: [filter]
  - id: notes
    run: echo notes
`

func loadReuseDAG(t *testing.T, yaml string) *ir.DAG {
	t.Helper()
	dag, err := spec.LoadYAML(t.Context(), []byte(yaml))
	require.NoError(t, err)
	return dag
}

func succeededRun(dag *ir.DAG) *ir.DAGRunStatus {
	status := &ir.DAGRunStatus{DAGRunID: "earlier", Status: ir.Succeeded, Params: strings.Join(dag.Params, " ")}
	for _, step := range dag.Steps {
		status.Nodes = append(status.Nodes, &ir.Node{Step: step, Status: ir.NodeSucceeded})
	}
	return status
}

func TestStepsToRun(t *testing.T) {
	t.Parallel()

	earlier := loadReuseDAG(t, reuseSpec)
	for _, tc := range []struct {
		name string
		yaml string
		// params replaces the parameters the run resolved, when set.
		params []string
		// profile is the runtime profile of the run.
		profile string
		// failed is a step that failed in the earlier run.
		failed string
		want   []string
	}{
		{name: "Unchanged", yaml: reuseSpec, want: nil},
		{name: "ChangedStepAndTheStepsAfterIt", yaml: strings.Replace(reuseSpec, "under 7000", "under 5000", 1), want: []string{"filter", "report"}},
		{name: "ChangedFirstStepReachesAStepThatOnlyReadsIt", yaml: strings.Replace(reuseSpec, "echo rows", "echo all rows", 1), want: []string{"page", "filter", "report"}},
		{name: "ChangedEnvironment", yaml: strings.Replace(reuseSpec, "homes.example.com", "homes.example.org", 1), want: []string{"page", "filter", "report", "notes"}},
		{name: "ChangedParameters", yaml: reuseSpec, params: []string{"MAX=5000"}, want: []string{"page", "filter", "report", "notes"}},
		{name: "ChangedProfile", yaml: reuseSpec, profile: "staging", want: []string{"page", "filter", "report", "notes"}},
		{name: "FailedStepAndTheStepsAfterIt", yaml: reuseSpec, failed: "filter", want: []string{"filter", "report"}},
		{name: "AddedStep", yaml: reuseSpec + "  - id: archive\n    run: echo archive\n", want: []string{"archive"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := succeededRun(earlier)
			for _, node := range source.Nodes {
				if node.Step.Name == tc.failed {
					node.Status = ir.NodeFailed
				}
			}
			dag := loadReuseDAG(t, tc.yaml)
			if tc.params != nil {
				dag.Params = tc.params
			}
			got, err := intake.StepsToRun(dag, tc.profile, earlier, source)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// A step carried from a run must be as StepsToRun would reuse it: a change of
// the step, a step it depends on, the parameters, the profile, or the settings
// outside the steps rejects it, and the error says which kind of change.
func TestCheckRunStepsUnchanged(t *testing.T) {
	t.Parallel()

	const settingsChanged = "step filter cannot be reused from run earlier: the parameters, runtime profile, base config, or top-level settings differ from that run"
	const stepChanged = "step filter cannot be reused from run earlier: the step or a step it depends on changed since that run"
	earlier := loadReuseDAG(t, reuseSpec)
	for _, tc := range []struct {
		name string
		yaml string
		// params replaces the parameters the run resolved, when set.
		params []string
		// profile is the runtime profile of the run.
		profile string
		// wantErr is empty when the step can be carried.
		wantErr string
	}{
		{name: "Unchanged", yaml: reuseSpec},
		{name: "ChangedStepThatItDoesNotNeed", yaml: strings.Replace(reuseSpec, "echo notes", "echo all notes", 1)},
		{name: "ChangedStep", yaml: strings.Replace(reuseSpec, "under 7000", "under 5000", 1), wantErr: stepChanged},
		{name: "ChangedStepThatItNeeds", yaml: strings.Replace(reuseSpec, "echo rows", "echo all rows", 1), wantErr: stepChanged},
		{name: "ChangedParameters", yaml: reuseSpec, params: []string{"MAX=5000"}, wantErr: settingsChanged},
		{name: "ChangedProfile", yaml: reuseSpec, profile: "staging", wantErr: settingsChanged},
		{name: "ChangedEnvironment", yaml: strings.Replace(reuseSpec, "homes.example.com", "homes.example.org", 1), wantErr: settingsChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dag := loadReuseDAG(t, tc.yaml)
			if tc.params != nil {
				dag.Params = tc.params
			}
			err := intake.CheckRunStepsUnchanged(dag, tc.profile,
				intake.RunStepSources{"filter": succeededRun(earlier)},
				intake.RunDefinitions{"earlier": earlier})
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.wantErr)
		})
	}
}

// A run keeps the base config written again without comments and without
// SMTP: only a change of a value counts.
func TestCheckRunStepsUnchangedBaseConfig(t *testing.T) {
	t.Parallel()

	const saved = "max_active_steps: 10\ntype: graph\n"
	for _, tc := range []struct {
		name    string
		base    string
		wantErr string
	}{
		{name: "SameValues", base: "# Base config\ntype: graph # model\nmax_active_steps: 10\nsmtp:\n  host: mail.example.com\n"},
		{name: "OtherValue", base: "type: graph\nmax_active_steps: 5\n", wantErr: "step filter cannot be reused from run earlier: the parameters, runtime profile, base config, or top-level settings differ from that run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			earlier := loadReuseDAG(t, reuseSpec)
			earlier.BaseConfigData = []byte(saved)
			dag := loadReuseDAG(t, reuseSpec)
			dag.BaseConfigData = []byte(tc.base)
			err := intake.CheckRunStepsUnchanged(dag, "",
				intake.RunStepSources{"filter": succeededRun(earlier)},
				intake.RunDefinitions{"earlier": earlier})
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.wantErr)
		})
	}
}

// One error names every changed step, so that a client can drop all of its
// stale entries after one refusal.
func TestCheckRunStepsUnchangedNamesEach(t *testing.T) {
	t.Parallel()

	earlier := loadReuseDAG(t, reuseSpec)
	run := succeededRun(earlier)
	dag := loadReuseDAG(t, strings.Replace(reuseSpec, "echo rows", "echo all rows", 1))
	err := intake.CheckRunStepsUnchanged(dag, "",
		intake.RunStepSources{"page": run, "filter": run, "notes": run},
		intake.RunDefinitions{"earlier": earlier})
	require.EqualError(t, err, "step filter cannot be reused from run earlier: the step or a step it depends on changed since that run; "+
		"step page cannot be reused from run earlier: the step or a step it depends on changed since that run")
}

// A step that the earlier run skipped by retry is reused when the caller
// supplied its outputs, which leaves it no finish time; one that carried
// nothing runs.
func TestStepsToRunSuppliedOutputs(t *testing.T) {
	t.Parallel()

	earlier := loadReuseDAG(t, reuseSpec)
	source := succeededRun(earlier)
	for _, node := range source.Nodes {
		switch node.Step.Name {
		case "page":
			node.Status, node.SkippedByRetry = ir.NodeSkipped, true
			node.OutputVariables = &collections.SyncMap{}
			node.OutputVariables.Store("ROWS", "ROWS=supplied")
		case "notes":
			node.Status, node.SkippedByRetry = ir.NodeSkipped, true
		}
	}
	got, err := intake.StepsToRun(loadReuseDAG(t, reuseSpec), "", earlier, source)
	require.NoError(t, err)
	require.Equal(t, []string{"notes"}, got)
}

// sharedBrowserSpec signs in and searches in one shared browser, and reads a
// report in a browser of its own.
const sharedBrowserSpec = `name: listings
steps:
  - id: sign_in
    action: browser.run
    with:
      url: https://homes.example.com/login
      browser: {share: homes}
      do:
        - wait: {selector: "form#login"}
  - id: search
    action: browser.run
    depends: [sign_in]
    with:
      browser: {share: homes}
      do:
        - goto: https://homes.example.com/search?max=70000000
  - id: report
    action: browser.run
    with:
      url: https://homes.example.com/report
      do:
        - wait: {selector: "#report"}
`

// A step that runs starts the browser it shares from a blank page, so the
// steps sharing that browser run with it, the earlier ones included; a
// browser of its own is unaffected.
func TestStepsToRunRunsTheStepsSharingABrowserTogether(t *testing.T) {
	t.Parallel()

	earlier := loadReuseDAG(t, sharedBrowserSpec)
	dag := loadReuseDAG(t, strings.Replace(sharedBrowserSpec, "max=70000000", "max=50000000", 1))
	got, err := intake.StepsToRun(dag, "", earlier, succeededRun(earlier))
	require.NoError(t, err)
	require.Equal(t, []string{"sign_in", "search"}, got)
}
