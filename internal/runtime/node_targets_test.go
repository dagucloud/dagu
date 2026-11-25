// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/stretchr/testify/require"
)

// fixedNodes is an inventory that holds the given nodes.
type fixedNodes []inventory.ResolvedNode

func (n fixedNodes) Select(ctx context.Context, _ string) ([]inventory.ResolvedNode, error) {
	return n.Nodes(ctx)
}

func (n fixedNodes) Nodes(context.Context) ([]inventory.ResolvedNode, error) {
	return n, nil
}

// A retry keeps the nodes the first attempt recorded, as the inventory holds
// them now (RFC 013 §Retry).
func TestRecordedNodes(t *testing.T) {
	t.Parallel()

	records := ir.NodeRuns{Runs: []ir.NodeRun{
		{Node: "web01", Status: ir.NodeRunSucceeded, DAGRunID: "run-1"},
		{Node: "web02", Status: ir.NodeRunPartiallySucceeded, DAGRunID: "run-2"},
		{Node: "web03", Status: ir.NodeRunFailed, DAGRunID: "run-3"},
		{Node: "web04", Status: ir.NodeRunNotStarted, DAGRunID: "run-4"},
		{Node: "web05", Status: ir.NodeRunAborted, DAGRunID: "run-5"},
		{Node: "WEB06", Status: ir.NodeRunDisabled},
	}}
	ctx := NewContext(context.Background(), &ir.DAG{Name: "deploy"}, "run", "",
		WithNodeSelector(fixedNodes{
			// web01 left, but its run succeeded; web04 left; web05 is disabled
			// now; web06, disabled before, is enabled; web07 is new.
			{Name: "web02", Disabled: true},
			{Name: "web03"},
			{Name: "web05", Disabled: true},
			{Name: "web06"},
			{Name: "web07"},
		}))

	runs, err := recordedNodes(ctx, records)
	require.NoError(t, err)
	require.Equal(t, []executor.NodeRun{
		{Node: "web01", Reuse: true, Run: executor.RunParams{RunID: "run-1"}},
		{Node: "web02", Reuse: true, Run: executor.RunParams{RunID: "run-2"}},
		{Node: "web03", Run: executor.RunParams{RunID: "run-3"}},
		{Node: "web04", Removed: true, Run: executor.RunParams{RunID: "run-4"}},
		{Node: "web05", Disabled: true, Run: executor.RunParams{RunID: "run-5"}},
		{Node: "WEB06"},
	}, runs)
}

// A retry whose recorded nodes can neither run nor keep a result fails.
func TestRecordedNodesNoneCanRun(t *testing.T) {
	t.Parallel()

	records := ir.NodeRuns{Runs: []ir.NodeRun{
		{Node: "web01", Status: ir.NodeRunFailed, DAGRunID: "run-1"},
		{Node: "web02", Status: ir.NodeRunNotStarted, DAGRunID: "run-2"},
	}}
	ctx := NewContext(context.Background(), &ir.DAG{Name: "deploy"}, "run", "",
		WithNodeSelector(fixedNodes{{Name: "web02", Disabled: true}}))

	_, err := recordedNodes(ctx, records)
	require.ErrorContains(t, err, "none of the 2 nodes the step ran on before can run")
}
