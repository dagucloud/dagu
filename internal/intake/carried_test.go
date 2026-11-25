// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake_test

import (
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/intake"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/require"
)

// A step carried from the source run counts as one named in outputsFromRuns
// does, and the caller can consent to reuse it anyway.
func TestPlanSelectedStepsChecksTheSource(t *testing.T) {
	t.Parallel()

	earlier := loadReuseDAG(t, reuseSpec)
	dag := loadReuseDAG(t, strings.Replace(reuseSpec, "echo rows", "echo all rows", 1))
	plan, err := intake.PlanSelectedSteps(dag, "", intake.StepSelection{
		Steps:            []string{"report"},
		Source:           succeededRun(earlier),
		SourceDefinition: earlier,
	})
	require.NoError(t, err)

	require.Equal(t, []intake.CarriedStep{
		{Step: "page", Ref: "page", DAGRunID: "earlier", Changes: []ir.StepChange{ir.StepChangeStep}},
		{Step: "filter", Ref: "filter", DAGRunID: "earlier", Changes: []ir.StepChange{ir.StepChangeDependency}},
	}, plan.Carried)
	require.EqualError(t, plan.Refusal(), "step filter cannot be reused from run earlier: a step it depends on changed since that run; "+
		"step page cannot be reused from run earlier: the step changed since that run")

	for _, tc := range []struct {
		name    string
		sel     intake.StepSelection
		wantErr string
	}{
		{name: "AllowedOne", sel: intake.StepSelection{AllowChanged: []string{"page"}}, wantErr: "step filter cannot be reused from run earlier: a step it depends on changed since that run"},
		{name: "AllowedEach", sel: intake.StepSelection{AllowChanged: []string{"page", "filter"}}},
		{name: "AllowedAll", sel: intake.StepSelection{AllowAllChanged: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sel := tc.sel
			sel.Steps, sel.Source, sel.SourceDefinition = []string{"report"}, succeededRun(earlier), earlier
			require.Equal(t, tc.wantErr, refusal(t, dag, "", sel))
		})
	}
}

// Consent names steps of the DAG.
func TestPlanSelectedStepsRejectsAnUnknownAllowedStep(t *testing.T) {
	t.Parallel()

	dag := loadReuseDAG(t, reuseSpec)
	_, err := intake.PlanSelectedSteps(dag, "", intake.StepSelection{Steps: []string{"report"}, AllowChanged: []string{"archive"}})
	require.ErrorContains(t, err, `allowChangedSteps: unknown step "archive"`)
}

// Only the steps whose outputs a selected step reads are checked: a changed
// step after the selection carries nothing and does not count.
func TestPlanSelectedStepsIgnoresTheStepsAfter(t *testing.T) {
	t.Parallel()

	earlier := loadReuseDAG(t, reuseSpec)
	dag := loadReuseDAG(t, strings.Replace(reuseSpec, "echo report", "echo final report", 1))
	plan, err := intake.PlanSelectedSteps(dag, "", intake.StepSelection{
		Steps:            []string{"filter"},
		Source:           succeededRun(earlier),
		SourceDefinition: earlier,
	})
	require.NoError(t, err)

	require.Equal(t, []intake.CarriedStep{
		{Step: "page", Ref: "page", DAGRunID: "earlier"},
	}, plan.Carried)
	require.Nil(t, plan.Refusal())
}

// An entry whose run cannot give the step is reported with the others
// instead of failing the plan, and the steps that nothing gives are missing.
func TestPlanSelectedStepsCollectsUnavailable(t *testing.T) {
	t.Parallel()

	dag := loadReuseDAG(t, reuseSpec)
	running := succeededRun(dag)
	running.DAGRunID = "running"
	running.Status = ir.Running
	plan, err := intake.PlanSelectedSteps(dag, "", intake.StepSelection{
		Steps:    []string{"report"},
		FromRuns: intake.RunStepSources{"page": running},
		Unavailable: []intake.CarriedStep{
			{Ref: "notes", DAGRunID: "gone", Unavailable: "the run does not exist for DAG listings"},
		},
		AllowAllChanged: true,
	})
	require.NoError(t, err)

	require.Equal(t, []string{"filter"}, plan.Missing)
	require.EqualError(t, plan.Refusal(),
		"step notes cannot be reused from run gone: the run does not exist for DAG listings; "+
			"step page cannot be reused from run running: the run is running, not finished")
}

// A run whose definition is not known cannot be compared; the caller can
// still consent to reuse its steps.
func TestPlanSelectedStepsUnknownDefinition(t *testing.T) {
	t.Parallel()

	dag := loadReuseDAG(t, reuseSpec)
	sel := intake.StepSelection{Steps: []string{"filter"}, Source: succeededRun(dag)}
	require.Equal(t, "step page cannot be reused from run earlier: what that run ran cannot be compared", refusal(t, dag, "", sel))
	sel.AllowChanged = []string{"page"}
	require.Empty(t, refusal(t, dag, "", sel))
}

// An unavailable entry still names a step, which must not be selected.
func TestPlanSelectedStepsRejectsASelectedStep(t *testing.T) {
	t.Parallel()

	dag := loadReuseDAG(t, reuseSpec)
	_, err := intake.PlanSelectedSteps(dag, "", intake.StepSelection{
		Steps:       []string{"report"},
		Unavailable: []intake.CarriedStep{{Ref: "report", DAGRunID: "gone", Unavailable: "the run does not exist for DAG listings"}},
	})
	require.EqualError(t, err, `outputsFromRuns names step "report", which is selected to run`)
}
