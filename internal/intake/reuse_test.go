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

// localDAGSpec is a local sub-DAG that a DAG file can define after its DAG.
const localDAGSpec = `---
name: archive
steps:
  - run: echo archive
`

// withHandler adds a handler of the event to the DAG spec, before its steps.
func withHandler(spec, event string) string {
	return strings.Replace(spec, "steps:", "handler_on:\n  "+event+":\n    run: echo "+event+"\nsteps:", 1)
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
		// consented is a step that the earlier run reused although it
		// changed.
		consented string
		want      []string
	}{
		{name: "Unchanged", yaml: reuseSpec, want: nil},
		{name: "ChangedStepAndTheStepsAfterIt", yaml: strings.Replace(reuseSpec, "under 7000", "under 5000", 1), want: []string{"filter", "report"}},
		{name: "ChangedFirstStepReachesAStepThatOnlyReadsIt", yaml: strings.Replace(reuseSpec, "echo rows", "echo all rows", 1), want: []string{"page", "filter", "report"}},
		{name: "ChangedEnvironment", yaml: strings.Replace(reuseSpec, "homes.example.com", "homes.example.org", 1), want: []string{"page", "filter", "report", "notes"}},
		{name: "ChangedParameters", yaml: reuseSpec, params: []string{"MAX=5000"}, want: []string{"page", "filter", "report", "notes"}},
		{name: "ChangedProfile", yaml: reuseSpec, profile: "staging", want: []string{"page", "filter", "report", "notes"}},
		{name: "FailedStepAndTheStepsAfterIt", yaml: reuseSpec, failed: "filter", want: []string{"filter", "report"}},
		{name: "AddedStep", yaml: reuseSpec + "  - id: archive\n    run: echo archive\n", want: []string{"archive"}},
		{name: "AddedSuccessHandler", yaml: withHandler(reuseSpec, "success"), want: nil},
		{name: "AddedInitHandler", yaml: withHandler(reuseSpec, "init"), want: []string{"page", "filter", "report", "notes"}},
		{name: "AddedLocalDAG", yaml: reuseSpec + localDAGSpec, want: []string{"page", "filter", "report", "notes"}},
		{name: "ConsentedStepAndTheStepsAfterIt", yaml: reuseSpec, consented: "page", want: []string{"page", "filter", "report"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := succeededRun(earlier)
			if tc.consented != "" {
				source.ReusedInputs = []ir.ReusedInput{{Step: tc.consented, RunID: "older", Via: ir.ReusedInputViaRun, Changes: []ir.StepChange{ir.StepChangeStep}}}
			}
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

// A step carried from a run must be as StepsToRun would reuse it, and the
// refusal names each kind of change.
func TestPlanSelectedStepsChanges(t *testing.T) {
	t.Parallel()

	const refused = "step filter cannot be reused from run earlier: "
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
		{name: "AddedSuccessHandler", yaml: withHandler(reuseSpec, "success")},
		// What describes, schedules or limits runs does not count.
		{name: "AddedMetadata", yaml: strings.Replace(reuseSpec, "env:", "description: Listings under a price\nlabels: [team=homes]\nschedule: \"0 * * * *\"\nhist_retention_days: 3\nmax_active_runs: 1\nenv:", 1)},
		{name: "ChangedStep", yaml: strings.Replace(reuseSpec, "under 7000", "under 5000", 1), wantErr: refused + "the step changed since that run"},
		{name: "ChangedStepThatItNeeds", yaml: strings.Replace(reuseSpec, "echo rows", "echo all rows", 1), wantErr: refused + "a step it depends on changed since that run"},
		{name: "ChangedParameters", yaml: reuseSpec, params: []string{"MAX=5000"}, wantErr: refused + "the parameters changed since that run"},
		// The default counts once, as the parameters the run resolves.
		{name: "ChangedParameterDefault", yaml: strings.Replace(reuseSpec, "MAX: 7000", "MAX: 5000", 1), wantErr: refused + "the parameters changed since that run"},
		{name: "ChangedProfile", yaml: reuseSpec, profile: "staging", wantErr: refused + "the runtime profile changed since that run"},
		{name: "ChangedEnvironment", yaml: strings.Replace(reuseSpec, "homes.example.com", "homes.example.org", 1), wantErr: refused + "the top-level settings changed since that run"},
		{name: "AddedInitHandler", yaml: withHandler(reuseSpec, "init"), wantErr: refused + "the top-level settings changed since that run"},
		{name: "AddedLocalDAG", yaml: reuseSpec + localDAGSpec, wantErr: refused + "the top-level settings changed since that run"},
		{name: "ChangedStepAndParameters", yaml: strings.Replace(reuseSpec, "under 7000", "under 5000", 1), params: []string{"MAX=5000"}, profile: "staging", wantErr: refused + "the step, the parameters, and the runtime profile changed since that run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dag := loadReuseDAG(t, tc.yaml)
			if tc.params != nil {
				dag.Params = tc.params
			}
			require.Equal(t, tc.wantErr, refusal(t, dag, tc.profile, intake.StepSelection{
				Steps:       []string{"report"},
				FromRuns:    intake.RunStepSources{"filter": succeededRun(earlier)},
				Definitions: intake.RunDefinitions{"earlier": earlier},
			}))
		})
	}
}

// A run keeps the base config as its snapshot writes it again: without
// comments, with SMTP taken out, and with an env mapping as a list. Only a
// change of a value other than SMTP and the handlers after the steps counts.
func TestPlanSelectedStepsBaseConfig(t *testing.T) {
	t.Parallel()

	const ran = "# Base config\ntype: graph # model\nmax_active_steps: 10\nenv:\n  REGION: eu\nsmtp:\n  host: mail.example.com\n"
	for _, tc := range []struct {
		name    string
		base    string
		wantErr string
	}{
		{name: "SameValues", base: ran},
		{name: "OtherSMTP", base: strings.Replace(ran, "mail.example.com", "mail.example.org", 1)},
		{name: "OtherSuccessHandler", base: ran + "handler_on:\n  success:\n    run: echo done\n"},
		{name: "OtherLimits", base: strings.Replace(ran, "max_active_steps: 10", "max_active_steps: 5", 1) + "hist_retention_days: 7\nmail_on:\n  failure: true\n"},
		{name: "OtherValue", base: strings.Replace(ran, "REGION: eu", "REGION: us", 1), wantErr: "step filter cannot be reused from run earlier: the base config changed since that run"},
		{name: "OtherInitHandler", base: ran + "handler_on:\n  init:\n    run: echo ready\n", wantErr: "step filter cannot be reused from run earlier: the base config changed since that run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			earlier := loadReuseDAG(t, reuseSpec)
			earlier.BaseConfigData = []byte(ran)
			snapshot, err := spec.PrepareDAGSnapshot(earlier)
			require.NoError(t, err)
			dag := loadReuseDAG(t, reuseSpec)
			dag.BaseConfigData = []byte(tc.base)
			require.Equal(t, tc.wantErr, refusal(t, dag, "", intake.StepSelection{
				Steps:       []string{"report"},
				FromRuns:    intake.RunStepSources{"filter": succeededRun(snapshot)},
				Definitions: intake.RunDefinitions{"earlier": snapshot},
			}))
		})
	}
}

// One refusal names every changed step.
func TestPlanSelectedStepsNamesEach(t *testing.T) {
	t.Parallel()

	earlier := loadReuseDAG(t, reuseSpec)
	run := succeededRun(earlier)
	dag := loadReuseDAG(t, strings.Replace(reuseSpec, "echo rows", "echo all rows", 1))
	require.Equal(t, "step filter cannot be reused from run earlier: a step it depends on changed since that run; "+
		"step page cannot be reused from run earlier: the step changed since that run",
		refusal(t, dag, "", intake.StepSelection{
			Steps:       []string{"report"},
			FromRuns:    intake.RunStepSources{"page": run, "filter": run, "notes": run},
			Definitions: intake.RunDefinitions{"earlier": earlier},
		}))
}

// refusal plans a run of sel and returns why it refuses its carried steps,
// empty when it takes all of them.
func refusal(t *testing.T, dag *ir.DAG, profile string, sel intake.StepSelection) string {
	t.Helper()
	plan, err := intake.PlanSelectedSteps(dag, profile, sel)
	require.NoError(t, err)
	if refused := plan.Refusal(); refused != nil {
		return refused.Error()
	}
	return ""
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
