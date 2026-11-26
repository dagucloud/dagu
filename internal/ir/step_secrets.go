// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

// Spec 092, "Secrets of a step" and "Untrusted steps". The rules in this file
// are the single source for the runtime, which filters the variables of a step,
// and for the coordinator, which gives a registry secret to a step.

// IsUntrusted reports whether the step runs as an untrusted step.
func (s Step) IsUntrusted() bool {
	return s.Untrusted
}

// HasUntrustedStep reports whether a step, a handler or a step in the body of
// a foreach step of the DAG is untrusted.
func (d *DAG) HasUntrustedStep() bool {
	if d == nil {
		return false
	}
	found := false
	d.WalkSteps(func(step *Step) {
		if step.IsUntrusted() {
			found = true
		}
	})
	return found
}

func (d *DAG) handlerSteps() []*Step {
	return []*Step{
		d.HandlerOn.Init,
		d.HandlerOn.Failure,
		d.HandlerOn.Success,
		d.HandlerOn.Abort,
		d.HandlerOn.Exit,
		d.HandlerOn.Wait,
	}
}

// WalkSteps calls visit for each step, each handler and each step in the
// body of a foreach step.
func (d *DAG) WalkSteps(visit func(*Step)) {
	var walk func(*Step)
	walk = func(step *Step) {
		if step == nil {
			return
		}
		visit(step)
		if step.Foreach != nil {
			for i := range step.Foreach.Steps {
				walk(&step.Foreach.Steps[i])
			}
		}
	}
	for i := range d.Steps {
		walk(&d.Steps[i])
	}
	for _, handler := range d.handlerSteps() {
		walk(handler)
	}
}
