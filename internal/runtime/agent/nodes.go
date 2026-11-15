// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// localWorkerID is the worker ID of a run on the Dagu host itself.
const localWorkerID = "local"

// nodeTargetingError fails, before any step starts, a run that uses node
// targeting (RFC 013) in a way that is not built yet:
//
//   - a node: required DAG started without a node;
//   - a run bound to a node that has a step running commands. The node is not
//     bound into the run over SSH yet, and running the step on the Dagu host
//     instead would send commands meant for the node to the wrong machine;
//   - a run on a worker with a step that sets nodes.
func nodeTargetingError(dag *ir.DAG, boundNode, workerID string) error {
	if dag.NodeRequired && boundNode == "" {
		return fmt.Errorf("DAG %q declares node: required, so it runs only bound to a node, as the child of a dag.run step with nodes", dag.Name)
	}
	onWorker := workerID != "" && workerID != localWorkerID
	for _, step := range allSteps(dag) {
		if boundNode != "" && runsCommands(step) {
			return fmt.Errorf("step %q runs commands, and running commands on inventory node %q is not available yet", step.Name, boundNode)
		}
		if onWorker && step.Nodes != nil {
			return fmt.Errorf("step %q sets nodes, and running a step on inventory nodes from a worker is not available yet", step.Name)
		}
	}
	return nil
}

// runsCommands reports whether a step runs commands, which reach the bound
// node of a run (RFC 013 §Bound runs).
func runsCommands(step ir.Step) bool {
	switch step.ExecutorConfig.Type {
	case "", "command", "shell", "ssh", "sftp":
		return true
	default:
		return false
	}
}

// allSteps returns the steps of dag, including foreach bodies and handlers.
func allSteps(dag *ir.DAG) []ir.Step {
	var steps []ir.Step
	var add func(ir.Step)
	add = func(step ir.Step) {
		steps = append(steps, step)
		if step.Foreach != nil {
			for _, child := range step.Foreach.Steps {
				add(child)
			}
		}
	}
	for _, step := range dag.Steps {
		add(step)
	}
	for _, handler := range []*ir.Step{dag.HandlerOn.Init, dag.HandlerOn.Failure, dag.HandlerOn.Success, dag.HandlerOn.Abort, dag.HandlerOn.Exit, dag.HandlerOn.Wait} {
		if handler != nil {
			add(*handler)
		}
	}
	return steps
}
