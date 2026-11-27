// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

var (
	// ErrNoCoordinator reports a run bound to a worker node where no
	// coordinator is configured, so the run cannot reach its worker.
	ErrNoCoordinator = errors.New("a run bound to a worker node reaches its worker through a coordinator, and none is configured")
	// ErrWorkerNodeChanged reports a run started bound to a worker node
	// whose name no longer names that worker node, as when the worker was
	// forgotten or an inventory node took the name.
	ErrWorkerNodeChanged = errors.New("the run was started bound to that worker node, which it no longer names")
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
	return listed, nil
}

// BoundWorker returns the worker that runs a run of dag narrowed to names:
// the worker node a node: required DAG is bound to, as nodes holds it when
// the run is dispatched. admitted is the worker node the run was started
// bound to (ir.DAGRunStatus.BoundWorker), or empty for a run being started.
//
// It returns "" for a run bound to an SSH node or to no node, and for a
// new run, to a name that is no node, which runs where any other run does
// and fails before any step. A run started bound to a worker node goes only
// to that worker: it fails with ErrWorkerNodeChanged once the name no
// longer names that worker node. A run bound to a worker node reaches it
// only through a coordinator, so without one it fails with
// ErrNoCoordinator.
func BoundWorker(ctx context.Context, dag *ir.DAG, names []string, nodes runtime.NodeSelector, hasCoordinator bool, admitted string) (string, error) {
	if !dag.NodeRequired || len(names) != 1 {
		return "", nil
	}
	worker := admitted
	if nodes != nil {
		found, err := nodes.Named(ctx, names)
		if err != nil {
			return "", fmt.Errorf("failed to read node %q: %w", names[0], err)
		}
		i := slices.IndexFunc(found, func(node inventory.ResolvedNode) bool {
			return strings.EqualFold(node.Name, names[0])
		})
		worker = ""
		if i >= 0 && found[i].Kind == inventory.NodeKindWorker {
			worker = found[i].Name
		}
	}
	if admitted != "" && !strings.EqualFold(worker, admitted) {
		return "", fmt.Errorf("node %q: %w", admitted, ErrWorkerNodeChanged)
	}
	if worker != "" && !hasCoordinator {
		return "", fmt.Errorf("node %q is a worker node: %w", worker, ErrNoCoordinator)
	}
	return worker, nil
}
