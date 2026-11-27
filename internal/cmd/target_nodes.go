// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"slices"
	"strings"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/intake"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
)

// flagNode narrows a run to the named nodes (RFC 013 §Narrowing a
// run), as repeatable names.
const flagNode = "node"

// errNodeWithFromRunID refuses --node on a new run from a run, which keeps
// the node list of that run, as it keeps its params.
var errNodeWithFromRunID = fmt.Errorf("--%s cannot be combined with --from-run-id: the new run keeps the node list of the run it is from", flagNode)

var nodeFlag = commandLineFlag{
	name:          flagNode,
	usage:         "Node the run is narrowed to: its steps with nodes run only on listed nodes, and a node: required DAG runs bound to the one node; repeatable",
	isStringArray: true,
}

// nodeFlagValues returns the --node flags as given, or nil when none were.
func nodeFlagValues(ctx *Context) ([]string, error) {
	names, err := ctx.Command.Flags().GetStringArray(flagNode)
	if err != nil {
		return nil, fmt.Errorf("failed to read node flags: %w", err)
	}
	// pflag drops an empty value; the flag still counts as changed.
	if ctx.Command.Flags().Changed(flagNode) &&
		(len(names) == 0 || slices.ContainsFunc(names, func(name string) bool { return strings.TrimSpace(name) == "" })) {
		return nil, fmt.Errorf("invalid --%s %q: expected a node name", flagNode, "")
	}
	return names, nil
}

// remoteTargetNodes renders the --node flags for a remote start or enqueue
// request, or nil when none were given. The server checks them.
func remoteTargetNodes(ctx *Context) (*api.TargetNodes, error) {
	names, err := nodeFlagValues(ctx)
	if err != nil || len(names) == 0 {
		return nil, err
	}
	list := api.TargetNodes(names)
	return &list, nil
}

// narrowRun checks the node list a run starts with against the project's
// nodes and returns the list the run carries, or nil without one.
func narrowRun(ctx *Context, dag *ir.DAG, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	nodes, err := ctx.inventoryStore()
	if err != nil {
		return nil, fmt.Errorf("failed to open the node inventory: %w", err)
	}
	known, err := inventory.NewProjectNodes(nodes, ctx.runtimeWorkerNodeStore()).Nodes(ctx.Context)
	if err != nil {
		return nil, fmt.Errorf("failed to read the node inventory: %w", err)
	}
	listed, err := intake.CheckTargetNodes(dag, names, known)
	if err != nil {
		return nil, fmt.Errorf("invalid node list: %w", err)
	}
	return listed, nil
}

// boundWorker returns the worker that runs a run of dag with the node list
// names (intake.BoundWorker), with a client of the coordinator that reaches
// it, or "" and nil for a run not bound to a worker node. admitted is the
// worker node the run was started bound to, or empty for a new run.
func boundWorker(ctx *Context, dag *ir.DAG, names []string, admitted string) (string, coordinator.Client, error) {
	worker, err := readBoundWorker(ctx, dag, names, ctx.coordinatorEnabled(), admitted)
	if err != nil || worker == "" {
		return "", nil, err
	}
	client, err := ctx.coordinatorClient()
	if err != nil {
		return "", nil, err
	}
	return worker, client, nil
}

// readBoundWorker reads the worker node a run of dag with the node list
// names is bound to (intake.BoundWorker).
func readBoundWorker(ctx *Context, dag *ir.DAG, names []string, hasCoordinator bool, admitted string) (string, error) {
	if !dag.NodeRequired || len(names) == 0 {
		return "", nil
	}
	nodes, err := ctx.inventoryStore()
	if err != nil {
		return "", fmt.Errorf("failed to open the node inventory: %w", err)
	}
	return intake.BoundWorker(ctx, dag, names, inventory.NewProjectNodes(nodes, ctx.runtimeWorkerNodeStore()), hasCoordinator, admitted)
}
