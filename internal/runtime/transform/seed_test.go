// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package transform_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/transform"
	"github.com/stretchr/testify/require"
)

func TestSeedNodesHumanTask(t *testing.T) {
	outputs := `{"decision":"approve"}`
	dag := &ir.DAG{Steps: []ir.Step{{Name: "review"}, {Name: "publish"}}}
	source := &ir.DAGRunStatus{Nodes: []*ir.Node{{
		Step:                   ir.Step{Name: "review"},
		Status:                 ir.NodeSucceeded,
		HumanTaskInput:         json.RawMessage(`{"decision":"approve"}`),
		HumanTaskCompletedBy:   "Alice",
		HumanTaskCompletedByID: "user-1",
		StepOutputsValue:       &outputs,
	}}}

	nodes := transform.SeedNodes(dag, source, []string{"review"})

	require.Len(t, nodes, 2)
	state := nodes[0].State
	require.Equal(t, ir.NodeSkipped, state.Status)
	require.True(t, state.SkippedByRetry)
	require.JSONEq(t, `{"decision":"approve"}`, string(state.HumanTaskInput))
	require.Equal(t, "Alice", state.HumanTaskCompletedBy)
	require.Equal(t, "user-1", state.HumanTaskCompletedByID)
	require.NotNil(t, state.StepOutputsValue)
	require.JSONEq(t, outputs, *state.StepOutputsValue)
	require.Equal(t, ir.NodeNotStarted, nodes[1].State.Status)
}

// A run seeded from another starts with nothing spent on models: the steps
// it skips spent theirs in the run they came from.
func TestSeedNodesLeavesModelUsage(t *testing.T) {
	dag := &ir.DAG{Steps: []ir.Step{{Name: "ask"}}}
	source := &ir.DAGRunStatus{Nodes: []*ir.Node{{
		Step:       ir.Step{Name: "ask"},
		Status:     ir.NodeSucceeded,
		ModelUsage: &ir.StepModelUsage{Models: []ir.ModelUsage{{Provider: "openai", Model: "gpt", Calls: 2}}},
	}}}

	nodes := transform.SeedNodes(dag, source, []string{"ask"})

	require.Len(t, nodes, 1)
	require.Nil(t, nodes[0].State.ModelUsage)
}

func TestSeedNodesNoSource(t *testing.T) {
	dag := &ir.DAG{Steps: []ir.Step{{Name: "build"}, {Name: "test"}}}

	nodes := transform.SeedNodes(dag, nil, []string{"build"})

	require.Len(t, nodes, 2)
	require.Equal(t, ir.NodeSkipped, nodes[0].State.Status)
	require.True(t, nodes[0].State.SkippedByRetry)
	require.Equal(t, ir.NodeNotStarted, nodes[1].State.Status)
}

// A failed attempt publishes no outputs, so a skipped step seeded from one
// must not carry the partial output it recorded.
func TestSeedNodesFailedSource(t *testing.T) {
	partial := `{"token":"partial"}`
	dag := &ir.DAG{Steps: []ir.Step{{Name: "build"}}}
	source := &ir.DAGRunStatus{Nodes: []*ir.Node{{
		Step:             ir.Step{Name: "build"},
		Status:           ir.NodeFailed,
		StepOutputsValue: &partial,
	}}}

	nodes := transform.SeedNodes(dag, source, []string{"build"})

	require.Equal(t, ir.NodeSkipped, nodes[0].State.Status)
	require.Nil(t, nodes[0].State.StepOutputsValue)
}

// Step retry and edited-DAG retry seed nodes from the earlier status. The
// masked outputs go with the restored outputs, but only for the steps that
// carry them.
func TestSeedNodesCarryMaskedOutputs(t *testing.T) {
	dag := &ir.DAG{Steps: []ir.Step{{Name: "fetch"}, {Name: "other"}}}
	masked := &ir.MaskedOutputs{Names: []string{"token"}}
	source := &ir.DAGRunStatus{Nodes: []*ir.Node{
		{Step: ir.Step{Name: "fetch"}, Status: ir.NodeSucceeded, MaskedOutputs: masked},
		{Step: ir.Step{Name: "other"}, Status: ir.NodeSucceeded, MaskedOutputs: masked},
	}}

	nodes := transform.SeedNodes(dag, source, []string{"fetch"})
	require.Equal(t, masked, nodes[0].State.MaskedOutputs)
	require.NotSame(t, masked, nodes[0].State.MaskedOutputs)
	require.Nil(t, nodes[1].State.MaskedOutputs)

	nodes = transform.SeedNodesCarrying(dag, source, []string{"fetch", "other"}, map[string]struct{}{"fetch": {}})
	require.Equal(t, masked, nodes[0].State.MaskedOutputs)
	require.Nil(t, nodes[1].State.MaskedOutputs)
}

// A plain retry restores nodes with ToNode, and the status written afterwards
// converts them back, so the masked outputs survive both ways.
func TestNodeMaskedOutputsRoundTrip(t *testing.T) {
	original := &ir.Node{
		Step:          ir.Step{Name: "fetch"},
		Status:        ir.NodeSucceeded,
		MaskedOutputs: &ir.MaskedOutputs{Names: []string{"TOKEN"}, All: true},
	}

	restored := transform.ToNode(original)
	require.Equal(t, original.MaskedOutputs, restored.State().MaskedOutputs)

	dag := &ir.DAG{Name: "test", Steps: []ir.Step{original.Step}}
	status := ir.NewStatusBuilder(dag).Create("run-1", ir.Succeeded, 0, time.Now(),
		transform.WithNodes([]runtime.NodeData{{Step: original.Step, State: restored.State()}}))
	require.Equal(t, original.MaskedOutputs, status.Nodes[0].MaskedOutputs)
}
