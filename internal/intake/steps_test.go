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

			nodes, err := intake.SelectedStepNodes(selectedStepsDAG(tt.dagType), tt.steps, tt.source, nil)
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

		nodes, err := intake.SelectedStepNodes(selectedStepsDAG(""), []string{"report"}, finished, nil)
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

			nodes, err := intake.SelectedStepNodes(outputsDAG(), []string{"report"}, tt.source, tt.outputs)
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
