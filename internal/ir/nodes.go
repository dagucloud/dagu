// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"fmt"
	"slices"
)

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
