// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// unsupportedNodeTargeting fails a run that uses node targeting (RFC 013)
// before any step starts. The fields validate, but running a step on its
// nodes is not built yet, and running it on the Dagu host instead would
// send commands meant for remote hosts to the wrong machine.
func unsupportedNodeTargeting(dag *ir.DAG) error {
	if dag.NodeRequired {
		return fmt.Errorf("DAG %q declares node: required, and running a DAG bound to a node is not available yet", dag.Name)
	}
	for _, step := range allSteps(dag) {
		if step.Nodes != nil {
			return fmt.Errorf("step %q sets nodes, and running a step on inventory nodes is not available yet", step.Name)
		}
	}
	return nil
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
