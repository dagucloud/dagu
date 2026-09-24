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
