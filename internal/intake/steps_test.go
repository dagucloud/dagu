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

			nodes, err := intake.SelectedStepNodes(selectedStepsDAG(tt.dagType), tt.steps, tt.source)
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

		nodes, err := intake.SelectedStepNodes(selectedStepsDAG(""), []string{"report"}, finished)
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
