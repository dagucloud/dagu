// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"context"
	"errors"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/require"
)

func TestCheckTargetNodes(t *testing.T) {
	t.Parallel()

	known := []inventory.ResolvedNode{
		{Name: "db01"}, {Name: "tokyo-gw", Kind: inventory.NodeKindWorker}, {Name: "web01"}, {Name: "web02", Disabled: true},
	}
	nodeStep := &ir.DAG{Name: "deploy", Steps: []ir.Step{{Name: "restart", Nodes: &ir.NodeTargets{Selector: "web"}}}}
	childStep := &ir.DAG{Name: "rollout", Steps: []ir.Step{{Name: "deploy", SubDAG: &ir.SubDAG{Name: "deploy-one"}}}}
	inForeach := &ir.DAG{Name: "each", Steps: []ir.Step{{Name: "each", Foreach: &ir.ForeachConfig{
		Steps: []ir.Step{{Name: "run", SubDAG: &ir.SubDAG{Name: "deploy-one"}}},
	}}}}
	inHandler := &ir.DAG{Name: "notify", Steps: []ir.Step{{Name: "a"}}}
	inHandler.HandlerOn.Exit = &ir.Step{Name: "onExit", Nodes: &ir.NodeTargets{Selector: "web"}}
	bound := &ir.DAG{Name: "deploy-one", NodeRequired: true, Steps: []ir.Step{{Name: "a"}}}
	plain := &ir.DAG{Name: "plain", Steps: []ir.Step{{Name: "a"}}}
	onWorkers := &ir.DAG{Name: "remote", WorkerSelector: map[string]string{"gpu": "true"}, Steps: nodeStep.Steps}

	for _, tc := range []struct {
		name    string
		dag     *ir.DAG
		names   []string
		want    []string
		wantErr string
	}{
		{name: "no list", dag: plain},
		// Names are matched without case, sorted, and kept once each.
		{name: "step with nodes", dag: nodeStep, names: []string{"WEB01", "db01", "web01"}, want: []string{"db01", "web01"}},
		{name: "a disabled node is a node", dag: nodeStep, names: []string{"web02"}, want: []string{"web02"}},
		{name: "a worker node is a node", dag: nodeStep, names: []string{"Tokyo-GW", "web01"}, want: []string{"tokyo-gw", "web01"}},
		{name: "dag.run step", dag: childStep, names: []string{"web01"}, want: []string{"web01"}},
		{name: "dag.run in a foreach body", dag: inForeach, names: []string{"web01"}, want: []string{"web01"}},
		{name: "handler with nodes", dag: inHandler, names: []string{"web01"}, want: []string{"web01"}},
		{name: "node: required with one node", dag: bound, names: []string{"web01", "WEB01"}, want: []string{"web01"}},
		{name: "unknown name", dag: nodeStep, names: []string{"web1"}, wantErr: `node "web1" is not a node of the project`},
		{name: "nothing to narrow", dag: plain, names: []string{"web01"}, wantErr: "has no step with nodes and no dag.run step"},
		{name: "node: required with two nodes", dag: bound, names: []string{"web01", "db01"}, wantErr: "starts with exactly one node, not 2"},
		{name: "node: required with no node", dag: bound, wantErr: "starts with exactly one node, not 0"},
		{name: "node: required with a worker node", dag: bound, names: []string{"TOKYO-GW"}, want: []string{"tokyo-gw"}},
		{name: "worker selector", dag: onWorkers, names: []string{"web01"}, want: []string{"web01"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := CheckTargetNodes(tc.dag, tc.names, known)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// fixedNodes is a project of fixed nodes, read by name as the store reads
// them.
type fixedNodes []inventory.ResolvedNode

func (n fixedNodes) Select(context.Context, string) ([]inventory.ResolvedNode, error) {
	return nil, errors.New("not used")
}

func (n fixedNodes) Named(_ context.Context, names []string) ([]inventory.ResolvedNode, error) {
	return inventory.Named(n, names), nil
}

func TestBoundWorker(t *testing.T) {
	t.Parallel()

	nodes := fixedNodes{{Name: "tokyo-gw", Kind: inventory.NodeKindWorker}, {Name: "web01"}}
	bound := &ir.DAG{Name: "deploy-one", NodeRequired: true}
	nodeStep := &ir.DAG{Name: "deploy", Steps: []ir.Step{{Name: "restart", Nodes: &ir.NodeTargets{Selector: "web"}}}}

	for _, tc := range []struct {
		name          string
		dag           *ir.DAG
		names         []string
		nodes         fixedNodes
		noCoordinator bool
		admitted      string
		want          string
		wantErr       error
	}{
		{name: "bound to a worker node", dag: bound, names: []string{"Tokyo-GW"}, nodes: nodes, want: "tokyo-gw"},
		{name: "bound to an SSH node", dag: bound, names: []string{"web01"}, nodes: nodes},
		// The agent fails such a run before any step.
		{name: "a new run bound to no node", dag: bound, names: []string{"osaka-gw"}, nodes: nodes},
		{name: "not bound", dag: nodeStep, names: []string{"tokyo-gw"}, nodes: nodes},
		{name: "no coordinator", dag: bound, names: []string{"tokyo-gw"}, nodes: nodes, noCoordinator: true, wantErr: ErrNoCoordinator},
		{name: "an SSH node needs no coordinator", dag: bound, names: []string{"web01"}, nodes: nodes, noCoordinator: true},
		{name: "started bound to its worker", dag: bound, names: []string{"tokyo-gw"}, nodes: nodes, admitted: "tokyo-gw", want: "tokyo-gw"},
		// An inventory node wins a name from a worker.
		{name: "an inventory node took the name", dag: bound, names: []string{"web01"}, nodes: nodes, admitted: "web01", wantErr: ErrWorkerNodeChanged},
		{name: "the worker was forgotten", dag: bound, names: []string{"osaka-gw"}, nodes: nodes, admitted: "osaka-gw", wantErr: ErrWorkerNodeChanged},
		{name: "no nodes to read", dag: bound, names: []string{"tokyo-gw"}, admitted: "tokyo-gw", want: "tokyo-gw"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// A nil list stands for a run that reads no nodes.
			var selector runtime.NodeSelector
			if tc.nodes != nil {
				selector = tc.nodes
			}
			got, err := BoundWorker(context.Background(), tc.dag, tc.names, selector, !tc.noCoordinator, tc.admitted)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
