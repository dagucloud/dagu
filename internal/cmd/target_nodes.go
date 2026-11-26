// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"slices"
	"strings"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/intake"
	"github.com/dagucloud/dagu/v2/internal/ir"
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
// nodes and returns the list the run carries, or nil without one. A narrowed
// run runs on the Dagu host: steps with nodes do not run on workers.
func narrowRun(ctx *Context, dag *ir.DAG, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	nodes, err := ctx.inventoryStore()
	if err != nil {
		return nil, fmt.Errorf("failed to open the node inventory: %w", err)
	}
	known, err := projectNodes{inventories: nodes, workers: ctx.runtimeWorkerNodeStore()}.Nodes(ctx.Context)
	if err != nil {
		return nil, fmt.Errorf("failed to read the node inventory: %w", err)
	}
	listed, err := intake.CheckTargetNodes(dag, names, known)
	if err != nil {
		return nil, fmt.Errorf("invalid node list: %w", err)
	}
	dag.ForceLocal = true
	return listed, nil
}
