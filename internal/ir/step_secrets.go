// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import "slices"

// Spec 092, "Secrets of a step" and "Untrusted steps". The rules in this file
// are the single source for the runtime, which filters the variables of a step,
// and for the coordinator, which gives a registry secret to a step.

// IsUntrusted reports whether the step runs as an untrusted step. An agent.run
// step starts a CLI agent, so it is always untrusted.
func (s Step) IsUntrusted() bool {
	return s.Untrusted || s.Agent != nil
}

// HasSecretStepScope reports whether the DAG has step scope: a step of the DAG
// is untrusted or has the key secrets. In a DAG with step scope, a step gets
// only the secrets that its list names.
func (d *DAG) HasSecretStepScope() bool {
	if d == nil {
		return false
	}
	if d.SecretStepScope {
		return true
	}
	found := false
	d.WalkSteps(func(step *Step) {
		if step.IsUntrusted() || len(step.Secrets) > 0 {
			found = true
		}
	})
	return found
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

// StepSecretScope tells which secrets of the run a step gets.
type StepSecretScope struct {
	// Scoped is false when the step gets each secret of the run, as in Spec 069.
	Scoped bool
	// Names lists the secrets that the step gets when Scoped is true. It is
	// empty for an untrusted step and for a step with no list.
	Names []string
}

// Allows reports whether the step gets the secret name.
func (s StepSecretScope) Allows(name string) bool {
	return !s.Scoped || slices.Contains(s.Names, name)
}

// SecretScopeOf returns the secrets that the step gets. An untrusted step gets
// no secret. A step of a DAG with step scope gets the secrets of its list. A
// step of another DAG gets each secret.
func (d *DAG) SecretScopeOf(step Step) StepSecretScope {
	if step.IsUntrusted() {
		return StepSecretScope{Scoped: true}
	}
	if d.HasSecretStepScope() {
		return StepSecretScope{Scoped: true, Names: slices.Clone(step.Secrets)}
	}
	return StepSecretScope{}
}

// SecretStep returns the step or the handler of the DAG that has the name. A
// step in the body of a foreach step is not returned: it cannot list a secret.
func (d *DAG) SecretStep(name string) (Step, bool) {
	if d == nil || name == "" {
		return Step{}, false
	}
	for _, step := range d.Steps {
		if step.Name == name {
			return step, true
		}
	}
	for _, handler := range d.handlerSteps() {
		if handler != nil && handler.Name == name {
			return *handler, true
		}
	}
	return Step{}, false
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
