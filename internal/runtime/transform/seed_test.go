// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package transform_test

import (
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
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
