// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// CheckTargetNodes checks the node list a run is started or enqueued with
// (RFC 013 §Narrowing a run) against the project's nodes, and returns it as
// the inventory spells the names, in name order, each once, or nil for an
// empty list. Every error it returns is a mistake in the request.
//
// Every name must be a node of the project, so a mistyped name never starts
// a run. A node: required DAG takes exactly one node, which binds the run, so
// it refuses an empty list too; that node is an SSH node, since a run bound
// to a worker node starts only as the child of a step with nodes. Any other
// DAG takes a list only when a step sets nodes or runs a child DAG.
func CheckTargetNodes(dag *ir.DAG, names []string, known []inventory.ResolvedNode) ([]string, error) {
	if len(names) == 0 && !dag.NodeRequired {
		return nil, nil
	}
	if !dag.NodeRequired && !dag.HasNodeOrSubDAGSteps() {
		return nil, fmt.Errorf("DAG %q has no step with nodes and no dag.run step, so a node list narrows nothing", dag.Name)
	}
	var listed []string
	for _, name := range names {
		i := slices.IndexFunc(known, func(node inventory.ResolvedNode) bool {
			return strings.EqualFold(node.Name, strings.TrimSpace(name))
		})
		if i < 0 {
			return nil, fmt.Errorf("node %q is not a node of the project", name)
		}
		if !slices.Contains(listed, known[i].Name) {
			listed = append(listed, known[i].Name)
		}
	}
	slices.Sort(listed)
	if dag.NodeRequired && len(listed) != 1 {
		return nil, fmt.Errorf("DAG %q declares node: required, so it starts with exactly one node, not %d", dag.Name, len(listed))
	}
	if dag.NodeRequired && slices.ContainsFunc(known, func(node inventory.ResolvedNode) bool {
		return node.Name == listed[0] && node.Kind == inventory.NodeKindWorker
	}) {
		return nil, fmt.Errorf("node %q is a worker node, which runs a node: required DAG only as the child of a step with nodes", listed[0])
	}
	return listed, nil
}
