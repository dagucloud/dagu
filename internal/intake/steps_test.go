// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/intake"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/require"
)

func TestSelectedStepNodes(t *testing.T) {
	t.Parallel()

	output := `{"token":"abc"}`
	finished := &ir.DAGRunStatus{
		DAGRunID: "source",
		Status:   ir.Failed,
		Nodes: []*ir.Node{
			{Step: ir.Step{Name: "login"}, Status: ir.NodeSucceeded, StepOutputsValue: &output},
			{Step: ir.Step{Name: "fetch"}, Status: ir.NodeFailed},
			{Step: ir.Step{Name: "report"}, Status: ir.NodeNotStarted},
		},
	}

	tests := []struct {
		name    string
		dagType string
		steps   []string
		source  *ir.DAGRunStatus
		// want maps each step name to its seeded status.
		want    map[string]ir.NodeStatus
		wantErr string
	}{
		{
			name:  "ByName",
			steps: []string{"fetch"},
			want:  map[string]ir.NodeStatus{"login": ir.NodeSkipped, "fetch": ir.NodeNotStarted, "report": ir.NodeSkipped},
		},
		{
			name:  "ByID",
			steps: []string{"report_id"},
			want:  map[string]ir.NodeStatus{"login": ir.NodeSkipped, "fetch": ir.NodeSkipped, "report": ir.NodeNotStarted},
		},
		{
			name:  "Duplicate",
			steps: []string{"fetch", " fetch "},
			want:  map[string]ir.NodeStatus{"login": ir.NodeSkipped, "fetch": ir.NodeNotStarted, "report": ir.NodeSkipped},
		},
		{
			name:    "Unknown",
			steps:   []string{"missing"},
			wantErr: `unknown step "missing" (available: login, fetch, report)`,
		},
		{
			name:    "Empty",
			steps:   []string{" "},
			wantErr: "step name must not be empty",
		},
		{
			name:    "NoSteps",
			wantErr: "at least one step is required",
		},
		{
			name:    "Agent",
			dagType: ir.TypeAgent,
			steps:   []string{"fetch"},
			wantErr: "not supported for agent DAGs",
		},
		{
			name:    "ActiveSource",
			steps:   []string{"fetch"},
			source:  &ir.DAGRunStatus{DAGRunID: "busy", Status: ir.Running},
			wantErr: "dag-run busy is running",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			nodes, _, err := intake.SelectedStepNodes(selectedStepsDAG(tt.dagType), tt.steps, tt.source, nil, nil, nil)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			got := make(map[string]ir.NodeStatus, len(nodes))
			for _, node := range nodes {
				got[node.Step.Name] = node.State.Status
				if node.State.Status == ir.NodeSkipped {
					require.True(t, node.State.SkippedByRetry, node.Step.Name)
				}
			}
			require.Equal(t, tt.want, got)
		})
	}

	// Only a reusable source node lends its outputs; a failed one does not.
	t.Run("ReusesOutputs", func(t *testing.T) {
		t.Parallel()

		nodes, _, err := intake.SelectedStepNodes(selectedStepsDAG(""), []string{"report"}, finished, nil, nil, nil)
		require.NoError(t, err)
		require.NotNil(t, nodes[0].State.StepOutputsValue)
		require.JSONEq(t, output, *nodes[0].State.StepOutputsValue)
		require.Nil(t, nodes[1].State.StepOutputsValue)
	})
}

func selectedStepsDAG(dagType string) *ir.DAG {
	dag := &ir.DAG{
		Name: "selected",
		Type: dagType,
		Steps: []ir.Step{
			{Name: "login"},
			{Name: "fetch", Depends: []string{"login"}},
			{Name: "report", ID: "report_id", Depends: []string{"fetch"}},
		},
	}
	return dag
}

func TestSelectedStepNodesOutputs(t *testing.T) {
	t.Parallel()

	carried := `{"token":"old","claims":"{}"}`
	source := &ir.DAGRunStatus{
		DAGRunID: "source",
		Status:   ir.Succeeded,
		Nodes: []*ir.Node{
			{Step: ir.Step{Name: "Log in"}, Status: ir.NodeSucceeded, StepOutputsValue: &carried},
		},
	}

	tests := []struct {
		name    string
		source  *ir.DAGRunStatus
		outputs map[string]map[string]string
		// wantOutputs maps a step name to the published outputs it is seeded
		// with; wantVars maps a step name to its output variables.
		wantOutputs map[string]string
		wantVars    map[string]string
		wantErr     string
	}{
		{
			name:        "Declared",
			outputs:     map[string]map[string]string{"login": {"token": "abc", "claims": `{"sub":"u1"}`}},
			wantOutputs: map[string]string{"Log in": `{"token":"abc","claims":"{\"sub\":\"u1\"}"}`},
		},
		{
			name:        "OverridesSource",
			source:      source,
			outputs:     map[string]map[string]string{"Log in": {"token": "new"}},
			wantOutputs: map[string]string{"Log in": `{"token":"new","claims":"{}"}`},
		},
		{
			name:        "AnyNameWithoutContract",
			outputs:     map[string]map[string]string{"fetch": {"url": "https://example.com"}},
			wantOutputs: map[string]string{"fetch": `{"url":"https://example.com"}`},
		},
		{
			// A string-form output variable is not a named output, so it is
			// set only as the variable.
			name:     "OutputVariable",
			outputs:  map[string]map[string]string{"fetch": {"RESULT": "body"}},
			wantVars: map[string]string{"fetch": "RESULT=body"},
		},
		{
			name:    "Undeclared",
			outputs: map[string]map[string]string{"login": {"other": "x"}},
			wantErr: `step "login": output "other" is not declared by the step`,
		},
		{
			name:    "InvalidJSON",
			outputs: map[string]map[string]string{"login": {"claims": "{"}},
			wantErr: `output "claims" must be valid JSON`,
		},
		{
			name:    "InvalidName",
			outputs: map[string]map[string]string{"fetch": {"bad-name": "x"}},
			wantErr: `invalid output name "bad-name"`,
		},
		{
			name:    "NoID",
			outputs: map[string]map[string]string{"label": {"text": "x"}},
			wantErr: `output "text" cannot be referenced because the step has no id`,
		},
		{
			name:    "SelectedStep",
			outputs: map[string]map[string]string{"report": {"text": "x"}},
			wantErr: `cannot set outputs of step "report": it is selected to run`,
		},
		{
			name:    "UnknownStep",
			outputs: map[string]map[string]string{"missing": {"text": "x"}},
			wantErr: `unknown step "missing"`,
		},
		{
			name:    "SetTwice",
			outputs: map[string]map[string]string{"Log in": {"token": "a"}, "login": {"token": "b"}},
			wantErr: `output "token" of step "Log in" is set by both "Log in" and "login"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			nodes, _, err := intake.SelectedStepNodes(outputsDAG(), []string{"report"}, tt.source, nil, tt.outputs, nil)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			for _, node := range nodes {
				if want, ok := tt.wantOutputs[node.Step.Name]; ok {
					require.NotNil(t, node.State.StepOutputsValue, node.Step.Name)
					require.JSONEq(t, want, *node.State.StepOutputsValue, node.Step.Name)
				}
				if want, ok := tt.wantVars[node.Step.Name]; ok {
					require.Nil(t, node.State.StepOutputsValue, node.Step.Name)
					require.NotNil(t, node.State.OutputVariables, node.Step.Name)
					got, ok := node.State.OutputVariables.Load("RESULT")
					require.True(t, ok)
					require.Equal(t, want, got)
				}
			}
		})
	}
}

// outputsDAG has a step with an outputs contract whose name differs from its
// id, a step with only a string-form output variable, a step without an id,
// and the selected step.
func outputsDAG() *ir.DAG {
	return &ir.DAG{
		Name: "outputs",
		Steps: []ir.Step{
			{Name: "Log in", ID: "login", Outputs: []ir.StepOutputDeclaration{
				{Name: "token", Type: ir.StepDeclaredOutputTypeString},
				{Name: "claims", Type: ir.StepDeclaredOutputTypeJSON},
			}},
			{Name: "fetch", ID: "fetch", Output: "RESULT", Depends: []string{"Log in"}},
			{Name: "label", Depends: []string{"fetch"}},
			{Name: "report", ID: "report", Depends: []string{"label"}},
		},
	}
}

// Every step of outputs names its run, and only those steps do.
func TestCheckOutputSources(t *testing.T) {
	t.Parallel()

	values := map[string]string{"token": "x"}
	from := intake.OutputSource{RunID: "run-1"}

	tests := []struct {
		name    string
		outputs map[string]map[string]string
		sources map[string]intake.OutputSource
		wantErr string
	}{
		{name: "None"},
		{
			name:    "ByNameAndID",
			outputs: map[string]map[string]string{"Log in": values, "fetch": values},
			sources: map[string]intake.OutputSource{"login": from, "fetch": from},
		},
		{
			name:    "NoSources",
			outputs: map[string]map[string]string{"login": values},
		},
		{
			name:    "SourceForOneStep",
			outputs: map[string]map[string]string{"login": values, "fetch": values},
			sources: map[string]intake.OutputSource{"fetch": from},
		},
		{
			name:    "SourceWithoutOutputs",
			outputs: map[string]map[string]string{"login": values},
			sources: map[string]intake.OutputSource{"login": from, "fetch": from},
			wantErr: "outputsSources names step fetch, which is not in outputs",
		},
		{
			name:    "UnknownSourceStep",
			sources: map[string]intake.OutputSource{"missing": from},
			wantErr: `outputsSources: unknown step "missing"`,
		},
		{
			name:    "SourceGivenTwice",
			outputs: map[string]map[string]string{"login": values},
			sources: map[string]intake.OutputSource{"login": from, "Log in": from},
			wantErr: `outputsSources: step "Log in" is given by both`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := intake.CheckOutputSources(outputsDAG(), tt.outputs, tt.sources)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

// The provenance has one entry per skipped step that carried outputs, in DAG
// step order, and a supplied entry wins over the source run.
func TestSelectedStepNodesReusedInputs(t *testing.T) {
	t.Parallel()

	carried := `{"token":"old"}`
	source := &ir.DAGRunStatus{
		DAGRunID: "source",
		Status:   ir.Succeeded,
		Nodes: []*ir.Node{
			{Step: ir.Step{Name: "Log in"}, Status: ir.NodeSucceeded, StepOutputsValue: &carried, StartedAt: "2026-10-26T09:59:50Z", FinishedAt: "2026-10-26T10:00:00Z"},
			{Step: ir.Step{Name: "fetch"}, Status: ir.NodeSucceeded, StartedAt: "2026-10-26T10:00:30Z", FinishedAt: "2026-10-26T10:01:00Z"},
			{Step: ir.Step{Name: "label"}, Status: ir.NodeFailed, FinishedAt: "2026-10-26T10:02:00Z"},
		},
	}

	// Only the steps that a selected step depends on carry outputs. A step after
	// the selected one is skipped and carries nothing, even when the source run
	// has its outputs.
	t.Run("OnlyUpstreamCarries", func(t *testing.T) {
		t.Parallel()

		full := &ir.DAGRunStatus{
			DAGRunID: "source",
			Status:   ir.Succeeded,
			Nodes: []*ir.Node{
				{Step: ir.Step{Name: "Log in"}, Status: ir.NodeSucceeded, StartedAt: "2026-10-26T09:59:50Z", FinishedAt: "2026-10-26T10:00:00Z"},
				{Step: ir.Step{Name: "fetch"}, Status: ir.NodeSucceeded, StartedAt: "2026-10-26T10:00:30Z", FinishedAt: "2026-10-26T10:01:00Z"},
				{Step: ir.Step{Name: "label"}, Status: ir.NodeSucceeded, Stdout: "label.out", StartedAt: "2026-10-26T10:01:10Z", FinishedAt: "2026-10-26T10:02:00Z"},
				{Step: ir.Step{Name: "report"}, Status: ir.NodeSucceeded, Stdout: "report.out", StartedAt: "2026-10-26T10:02:10Z", FinishedAt: "2026-10-26T10:03:00Z"},
			},
		}

		nodes, reused, err := intake.SelectedStepNodes(outputsDAG(), []string{"fetch"}, full, nil, nil, nil)
		require.NoError(t, err)
		require.Equal(t, []ir.ReusedInput{
			{Step: "Log in", RunID: "source", CapturedAt: "2026-10-26T10:00:00Z", StartedAt: "2026-10-26T09:59:50Z", FinishedAt: "2026-10-26T10:00:00Z", Via: ir.ReusedInputViaRun},
		}, reused)
		for _, node := range nodes {
			switch node.Step.Name {
			case "label", "report":
				require.Equal(t, ir.NodeSkipped, node.State.Status)
				require.Empty(t, node.State.Stdout, node.Step.Name)
				require.True(t, node.State.StartedAt.IsZero(), node.Step.Name)
			}
		}
	})

	t.Run("FromRun", func(t *testing.T) {
		t.Parallel()

		_, reused, err := intake.SelectedStepNodes(outputsDAG(), []string{"report"}, source, nil, nil, nil)
		require.NoError(t, err)
		require.Equal(t, []ir.ReusedInput{
			{Step: "Log in", RunID: "source", CapturedAt: "2026-10-26T10:00:00Z", StartedAt: "2026-10-26T09:59:50Z", FinishedAt: "2026-10-26T10:00:00Z", Via: ir.ReusedInputViaRun},
			{Step: "fetch", RunID: "source", CapturedAt: "2026-10-26T10:01:00Z", StartedAt: "2026-10-26T10:00:30Z", FinishedAt: "2026-10-26T10:01:00Z", Via: ir.ReusedInputViaRun},
		}, reused)
	})

	t.Run("FromCache", func(t *testing.T) {
		t.Parallel()

		outputs := map[string]map[string]string{"login": {"token": "new"}}
		sources := map[string]intake.OutputSource{"login": {RunID: "gone", CapturedAt: "2026-10-25T08:00:00Z"}}
		_, reused, err := intake.SelectedStepNodes(outputsDAG(), []string{"report"}, nil, nil, outputs, sources)
		require.NoError(t, err)
		require.Equal(t, []ir.ReusedInput{
			{Step: "Log in", RunID: "gone", CapturedAt: "2026-10-25T08:00:00Z", Via: ir.ReusedInputViaCache},
		}, reused)
	})

	t.Run("CacheWinsOverRun", func(t *testing.T) {
		t.Parallel()

		outputs := map[string]map[string]string{"login": {"token": "new"}}
		sources := map[string]intake.OutputSource{"Log in": {RunID: "gone"}}
		_, reused, err := intake.SelectedStepNodes(outputsDAG(), []string{"report"}, source, nil, outputs, sources)
		require.NoError(t, err)
		require.Equal(t, []ir.ReusedInput{
			{Step: "Log in", RunID: "gone", Via: ir.ReusedInputViaCache},
			{Step: "fetch", RunID: "source", CapturedAt: "2026-10-26T10:01:00Z", StartedAt: "2026-10-26T10:00:30Z", FinishedAt: "2026-10-26T10:01:00Z", Via: ir.ReusedInputViaRun},
		}, reused)
	})

	// A supplied step without a source has no entry, also when the source run
	// carries it.
	t.Run("CacheWithoutSource", func(t *testing.T) {
		t.Parallel()

		outputs := map[string]map[string]string{"login": {"token": "new"}}
		_, reused, err := intake.SelectedStepNodes(outputsDAG(), []string{"report"}, source, nil, outputs, nil)
		require.NoError(t, err)
		require.Equal(t, []ir.ReusedInput{
			{Step: "fetch", RunID: "source", CapturedAt: "2026-10-26T10:01:00Z", StartedAt: "2026-10-26T10:00:30Z", FinishedAt: "2026-10-26T10:01:00Z", Via: ir.ReusedInputViaRun},
		}, reused)
	})

	t.Run("SelectedStepHasNone", func(t *testing.T) {
		t.Parallel()

		_, reused, err := intake.SelectedStepNodes(outputsDAG(), []string{"Log in", "fetch", "label", "report"}, source, nil, nil, nil)
		require.NoError(t, err)
		require.Empty(t, reused)
	})
}

// A step can carry its outputs from a run of its own, in place of the main
// source: the seeding is the same, the record names that run with the times
// that the step had there, and the checks name the step and the run.
func TestSelectedStepNodesFromRuns(t *testing.T) {
	t.Parallel()

	carried := `{"token":"old"}`
	newer := `{"token":"newer"}`
	main := &ir.DAGRunStatus{
		DAGRunID: "main",
		Status:   ir.Succeeded,
		Nodes: []*ir.Node{
			{Step: ir.Step{Name: "Log in"}, Status: ir.NodeSucceeded, StepOutputsValue: &carried, StartedAt: "2026-10-26T09:00:00Z", FinishedAt: "2026-10-26T09:00:10Z"},
			{Step: ir.Step{Name: "fetch"}, Status: ir.NodeSucceeded, StartedAt: "2026-10-26T09:00:10Z", FinishedAt: "2026-10-26T09:00:40Z"},
		},
	}
	other := &ir.DAGRunStatus{
		DAGRunID: "other",
		Status:   ir.Failed,
		Nodes: []*ir.Node{
			{Step: ir.Step{Name: "Log in"}, Status: ir.NodeSucceeded, StepOutputsValue: &newer, StartedAt: "2026-10-26T11:00:00Z", FinishedAt: "2026-10-26T11:00:20Z"},
			{Step: ir.Step{Name: "fetch"}, Status: ir.NodeFailed},
		},
	}

	t.Run("SeedsTheStepFromItsRun", func(t *testing.T) {
		t.Parallel()

		nodes, reused, err := intake.SelectedStepNodes(outputsDAG(), []string{"report"}, main, intake.RunStepSources{"login": other}, nil, nil)
		require.NoError(t, err)
		require.JSONEq(t, newer, *nodes[0].State.StepOutputsValue)
		require.Equal(t, []ir.ReusedInput{
			{Step: "Log in", RunID: "other", CapturedAt: "2026-10-26T11:00:20Z", StartedAt: "2026-10-26T11:00:00Z", FinishedAt: "2026-10-26T11:00:20Z", Via: ir.ReusedInputViaRun},
			{Step: "fetch", RunID: "main", CapturedAt: "2026-10-26T09:00:40Z", StartedAt: "2026-10-26T09:00:10Z", FinishedAt: "2026-10-26T09:00:40Z", Via: ir.ReusedInputViaRun},
		}, reused)
	})

	t.Run("WorksWithoutMainSource", func(t *testing.T) {
		t.Parallel()

		nodes, reused, err := intake.SelectedStepNodes(outputsDAG(), []string{"report"}, nil, intake.RunStepSources{"Log in": other}, nil, nil)
		require.NoError(t, err)
		require.JSONEq(t, newer, *nodes[0].State.StepOutputsValue)
		require.Len(t, reused, 1)
		require.Equal(t, "other", reused[0].RunID)
	})

	t.Run("SuppliedValuesStillWin", func(t *testing.T) {
		t.Parallel()

		outputs := map[string]map[string]string{"login": {"token": "mine"}}
		sources := map[string]intake.OutputSource{"login": {RunID: "gone"}}
		_, reused, err := intake.SelectedStepNodes(outputsDAG(), []string{"report"}, main, intake.RunStepSources{"login": other}, outputs, sources)
		require.NoError(t, err)
		require.Equal(t, ir.ReusedInputViaCache, reused[0].Via)
		require.Equal(t, "gone", reused[0].RunID)
	})

	tests := []struct {
		name    string
		steps   []string
		from    intake.RunStepSources
		wantErr string
	}{
		{
			name:    "StepDidNotSucceed",
			steps:   []string{"report"},
			from:    intake.RunStepSources{"fetch": other},
			wantErr: "step fetch cannot be reused from run other: the step did not succeed there",
		},
		{
			name:    "RunNotFinished",
			steps:   []string{"report"},
			from:    intake.RunStepSources{"fetch": {DAGRunID: "busy", Status: ir.Running}},
			wantErr: "step fetch cannot be reused from run busy: the run is running, not finished",
		},
		{
			name:    "StepNotInRun",
			steps:   []string{"report"},
			from:    intake.RunStepSources{"fetch": {DAGRunID: "thin", Status: ir.Succeeded}},
			wantErr: "step fetch cannot be reused from run thin: the run has no such step",
		},
		{
			name:    "StepSelectedToRun",
			steps:   []string{"fetch"},
			from:    intake.RunStepSources{"fetch": main},
			wantErr: `outputsFromRuns names step "fetch", which is selected to run`,
		},
		{
			name:    "UnknownStep",
			steps:   []string{"report"},
			from:    intake.RunStepSources{"missing": main},
			wantErr: `unknown step "missing"`,
		},
		{
			name:    "NameAndIDOfOneStep",
			steps:   []string{"report"},
			from:    intake.RunStepSources{"login": main, "Log in": main},
			wantErr: "is given by both",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := intake.SelectedStepNodes(outputsDAG(), tt.steps, nil, tt.from, nil, nil)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// A selected-steps run calls the end-of-run handlers only when the selection
// has a graph end step: a step that no other step depends on.
func TestSelectionSkipsHandlers(t *testing.T) {
	t.Parallel()

	chain := &ir.DAG{Steps: []ir.Step{
		{Name: "a", ID: "a_id"},
		{Name: "b", Depends: []string{"a"}},
		{Name: "c", Depends: []string{"b"}},
	}}
	diamond := &ir.DAG{Steps: []ir.Step{
		{Name: "a"},
		{Name: "b", Depends: []string{"a"}},
		{Name: "c", Depends: []string{"a"}},
		{Name: "d", Depends: []string{"b"}},
	}}
	inferred := &ir.DAG{Steps: []ir.Step{
		{Name: "produce"},
		{Name: "use", InferredDepends: []ir.InferredDependency{{Step: "produce"}}},
	}}

	tests := []struct {
		name    string
		dag     *ir.DAG
		steps   []string
		want    bool
		wantErr string
	}{
		{name: "InteriorStep", dag: chain, steps: []string{"b"}, want: true},
		{name: "EndStep", dag: chain, steps: []string{"c"}, want: false},
		{name: "FirstAndInterior", dag: chain, steps: []string{"a", "b"}, want: true},
		{name: "InteriorAndEnd", dag: chain, steps: []string{"a", "c"}, want: false},
		{name: "ByID", dag: chain, steps: []string{"a_id"}, want: true},
		{name: "DiamondEndStepOne", dag: diamond, steps: []string{"c"}, want: false},
		{name: "DiamondEndStepTwo", dag: diamond, steps: []string{"d"}, want: false},
		{name: "DiamondInterior", dag: diamond, steps: []string{"a", "b"}, want: true},
		{name: "InferredProducerIsInterior", dag: inferred, steps: []string{"produce"}, want: true},
		{name: "InferredConsumerIsEnd", dag: inferred, steps: []string{"use"}, want: false},
		{name: "Unknown", dag: chain, steps: []string{"x"}, wantErr: `unknown step "x"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := intake.SelectionSkipsHandlers(tt.dag, tt.steps)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
