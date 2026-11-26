// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"errors"
	"fmt"
	"slices"
)

// ErrNodeListOnWorkers refuses a dag.run child of a narrowed run that selects
// workers: the child runs where its parent runs, and adds to its parent's
// record of the list (RFC 013 §Narrowing a run).
var ErrNodeListOnWorkers = errors.New("a child of a run with a node list runs where its parent runs and cannot select workers")

// NodeTargets is a step's nodes field (RFC 013): which inventory nodes the
// step runs on, and how it dispatches them.
type NodeTargets struct {
	// Selector holds label filter terms (Spec 087) joined by commas. It may
	// hold references, which resolve when the step starts.
	Selector string `json:"selector"`
	// MaxConcurrent is how many nodes run at once.
	MaxConcurrent int `json:"maxConcurrent"`
	// KeepGoing starts the remaining nodes after one fails.
	KeepGoing bool `json:"keepGoing,omitempty"`
}

// HasNodeOrSubDAGSteps reports whether a step of the DAG, a foreach body or
// a handler included, sets nodes or runs a child DAG: the steps a run's node
// list can narrow.
func (d *DAG) HasNodeOrSubDAGSteps() bool {
	var narrows func(steps []Step) bool
	narrows = func(steps []Step) bool {
		for _, step := range steps {
			if step.Nodes != nil || step.SubDAG != nil {
				return true
			}
			if step.Foreach != nil && narrows(step.Foreach.Steps) {
				return true
			}
		}
		return false
	}
	if narrows(d.Steps) {
		return true
	}
	for _, handler := range []*Step{d.HandlerOn.Init, d.HandlerOn.Failure, d.HandlerOn.Success, d.HandlerOn.Abort, d.HandlerOn.Exit, d.HandlerOn.Wait} {
		if handler != nil && narrows([]Step{*handler}) {
			return true
		}
	}
	return false
}

const (
	// DefaultNodeMaxConcurrent runs one node at a time, so a command that
	// restarts a service never reaches every host at once by default.
	DefaultNodeMaxConcurrent = 1
)

// NodeStepChild returns the DAG that a bound child run of the command step
// stepName runs on one node (RFC 013): that step alone, in a node: required
// DAG that keeps this DAG's settings. The parent step keeps its
// dependencies, preconditions, continue_on, and output, and the node gives
// the connection, so the child drops them along with every other step,
// handler, and schedule.
func (d *DAG) NodeStepChild(stepName string) (*DAG, error) {
	idx := slices.IndexFunc(d.Steps, func(s Step) bool { return s.Name == stepName })
	if idx < 0 {
		return nil, fmt.Errorf("step %q not found in DAG %q", stepName, d.Name)
	}
	step := d.Steps[idx]
	if step.Nodes == nil || step.SubDAG != nil {
		return nil, fmt.Errorf("step %q is not a command step with nodes", stepName)
	}
	step.Nodes = nil
	step.Depends, step.Dependencies, step.InferredDepends = nil, nil, nil
	step.ExplicitlyNoDeps = false
	step.Preconditions = nil
	step.ContinueOn = ContinueOn{}
	step.Output, step.StructuredOutput, step.OutputSchema, step.Outputs = "", nil, nil, nil

	child := d.Clone()
	child.Steps = []Step{step}
	child.NodeRequired = true
	child.SSH, child.Container = nil, nil
	child.WorkerSelector, child.ForceLocal = nil, false
	child.HandlerOn = HandlerOn{}
	child.Preconditions = nil
	child.Schedule, child.StopSchedule, child.RestartSchedule = nil, nil, nil
	child.LocalDAGs = nil
	return child, nil
}
