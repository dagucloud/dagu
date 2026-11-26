// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func outputsDAG() *DAG {
	return &DAG{
		Outputs: map[string]DAGOutput{
			"summary":  {Step: "diagnose", Output: "summary"},
			"findings": {Step: "diagnose", Output: "findings"},
			"count":    {Step: "diagnose", Output: "count"},
			"quoted":   {Step: "diagnose", Output: "quoted"},
			"num":      {Step: "diagnose", Output: "num"},
			"nothing":  {Step: "diagnose", Output: "nothing"},
			"list":     {Step: "diagnose", Output: "list"},
			"bad":      {Step: "diagnose", Output: "bad"},
			"later":    {Step: "later", Output: "value"},
		},
		Steps: []Step{
			{ID: "diagnose", Outputs: []StepOutputDeclaration{
				{Name: "summary", Type: "string"},
				{Name: "findings", Type: "json"},
				{Name: "count", Type: "string"},
				{Name: "quoted", Type: "json"},
				{Name: "num", Type: "json"},
				{Name: "nothing", Type: "json"},
				{Name: "list", Type: "json"},
				{Name: "bad", Type: "json"},
			}},
			{ID: "later", Outputs: []StepOutputDeclaration{{Name: "value"}}},
		},
	}
}

func outputsStatus(status Status, published string) *DAGRunStatus {
	return &DAGRunStatus{Status: status, Nodes: []*Node{
		{Step: Step{ID: "diagnose"}, Status: NodeSucceeded, StepOutputsValue: &published},
		{Step: Step{ID: "later"}, Status: NodeSkipped},
	}}
}

// published is the stored shape: every declared output is a JSON string
// holding its text.
const published = `{"summary":"all good","findings":"{\"n\":1,\"items\":[\"a\"]}","count":"3",` +
	`"quoted":"\"x\"","num":"42","nothing":"null","list":"[1,2]","bad":"{not json"}`

func TestProjectDAGOutputs(t *testing.T) {
	t.Parallel()

	got := ProjectDAGOutputs(outputsDAG(), outputsStatus(Succeeded, published))
	encoded, err := json.Marshal(got)
	require.NoError(t, err)
	// later was skipped. count is a string output, so it stays a string; invalid
	// JSON text in a json output stays a string.
	require.JSONEq(t, `{"summary":"all good","findings":{"n":1,"items":["a"]},"count":"3",`+
		`"quoted":"x","num":42,"nothing":null,"list":[1,2],"bad":"{not json"}`, string(encoded))
}

func TestProjectDAGOutputsPartial(t *testing.T) {
	t.Parallel()

	require.Len(t, ProjectDAGOutputs(outputsDAG(), outputsStatus(PartiallySucceeded, published)), 8)
}

func TestProjectDAGOutputsUnsuccessful(t *testing.T) {
	t.Parallel()

	for _, status := range []Status{Failed, Aborted, Rejected, Queued, Running, Waiting, NotStarted} {
		require.Nil(t, ProjectDAGOutputs(outputsDAG(), outputsStatus(status, published)), status.String())
	}
}

func TestProjectDAGOutputsMissingValue(t *testing.T) {
	t.Parallel()

	got := ProjectDAGOutputs(outputsDAG(), outputsStatus(Succeeded, `{"summary":"ok"}`))
	require.Len(t, got, 1)
	require.JSONEq(t, `"ok"`, string(got["summary"]))
}

func TestProjectDAGOutputsMasked(t *testing.T) {
	t.Parallel()

	status := outputsStatus(Succeeded, published)
	status.Nodes[0].MaskedOutputs = &MaskedOutputs{Published: []string{"summary"}}
	got := ProjectDAGOutputs(outputsDAG(), status)
	require.NotContains(t, got, "summary")
	require.Contains(t, got, "findings")

	status.Nodes[0].MaskedOutputs = &MaskedOutputs{AllPublished: true}
	require.Nil(t, ProjectDAGOutputs(outputsDAG(), status))
}

func TestProjectDAGOutputsNone(t *testing.T) {
	t.Parallel()

	require.Nil(t, ProjectDAGOutputs(&DAG{}, outputsStatus(Succeeded, published)))
	require.Nil(t, ProjectDAGOutputs(nil, nil))
}

func TestProjectDAGOutputsRuntimeOnlyStep(t *testing.T) {
	t.Parallel()

	dag := &DAG{
		Outputs: map[string]DAGOutput{"r": {Step: "sub", Output: "n"}},
		Steps:   []Step{{ID: "sub"}},
	}
	value := `{"n":{"a":1}}`
	status := &DAGRunStatus{Status: Succeeded, Nodes: []*Node{{Step: Step{ID: "sub"}, Status: NodeSucceeded, StepOutputsValue: &value}}}
	require.JSONEq(t, `{"a":1}`, string(ProjectDAGOutputs(dag, status)["r"]))
}

// Captured outputs, such as those of outputs.write or stdout.outputs, are
// stored as JSON values and carry no declared type.
func TestProjectDAGOutputsCaptured(t *testing.T) {
	t.Parallel()

	dag := &DAG{
		Outputs: map[string]DAGOutput{
			"report": {Step: "pub", Output: "report"},
			"count":  {Step: "pub", Output: "count"},
		},
		Steps: []Step{{ID: "pub", Outputs: []StepOutputDeclaration{
			{Name: "report", Source: StepDeclaredOutputSourceCapture},
			{Name: "count", Source: StepDeclaredOutputSourceCapture},
		}}},
	}
	value := `{"report":{"ok":true},"count":3}`
	status := &DAGRunStatus{Status: Succeeded, Nodes: []*Node{{Step: Step{ID: "pub"}, Status: NodeSucceeded, StepOutputsValue: &value}}}
	encoded, err := json.Marshal(ProjectDAGOutputs(dag, status))
	require.NoError(t, err)
	require.JSONEq(t, `{"report":{"ok":true},"count":3}`, string(encoded))
}
