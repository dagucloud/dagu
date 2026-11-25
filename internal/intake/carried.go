// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

// StepSelection is what a run of selected steps runs and where its skipped
// steps take their outputs from.
type StepSelection struct {
	// Steps are the steps to run, by name or ID.
	Steps []string
	// Source is the run that the skipped steps carry outputs from, and
	// SourceDefinition the definition it ran, nil when it cannot be read.
	Source           *ir.DAGRunStatus
	SourceDefinition *ir.DAG
	// FromRuns names another run for single steps, Definitions holds the
	// definitions those runs ran, and Unavailable the entries whose run
	// could not be read (LoadRunStepSources).
	FromRuns    RunStepSources
	Definitions RunDefinitions
	Unavailable []CarriedStep
	// Outputs and Sources are outputs that the caller supplies for skipped
	// steps, and the runs they were captured from.
	Outputs map[string]map[string]string
	Sources map[string]OutputSource
	// AllowChanged names, by step name or ID, the carried steps that the
	// caller consents to reuse although they changed since their run, and
	// AllowAllChanged consents to every one of them.
	AllowChanged    []string
	AllowAllChanged bool
}

// SelectedStepsPlan is a run of selected steps before it starts: its seed
// nodes, where its skipped steps take their outputs from, and what changed
// for each of them since its run.
type SelectedStepsPlan struct {
	Nodes  []runtime.NodeData
	Reused []ir.ReusedInput
	// Carried lists, in the order of the DAG, each skipped step that carries
	// outputs from a run, and each entry of outputsFromRuns that its run
	// cannot give.
	Carried []CarriedStep
	// Missing lists, in the order of the DAG, the steps that a selected step
	// depends on and that take outputs from nowhere.
	Missing []string
	// allowed holds the names of the steps of AllowChanged.
	allowed  map[string]struct{}
	allowAll bool
}

// CarriedStep is a skipped step that carries outputs from a run into a run of
// selected steps.
type CarriedStep struct {
	// Step is the name of the step, and Ref the step as the caller named it.
	Step     string
	Ref      string
	DAGRunID string
	// Unavailable says why the run cannot give the step. It is empty when the
	// run can give it.
	Unavailable string
	// Changes lists what changed for the step since the run, in the order of
	// the ir.StepChange constants.
	Changes []ir.StepChange
	// StartedAt and FinishedAt are the times of the step in the run.
	StartedAt  string
	FinishedAt string
}

// Changed reports whether the run can give the step, which changed since.
func (c CarriedStep) Changed() bool {
	return c.Unavailable == "" && len(c.Changes) > 0
}

// Message says why the step cannot be reused from its run, as
// "step X cannot be reused from run Y: <reason>".
func (c CarriedStep) Message() string {
	reason := c.Unavailable
	if reason == "" {
		reason = changeReason(c.Changes)
	}
	return fmt.Sprintf("step %s cannot be reused from run %s: %s", c.Ref, c.DAGRunID, reason)
}

// RefusedStepsError reports the carried steps that a run of selected steps
// cannot take outputs from: first those whose run cannot give them, then
// those that changed since their run, each in the order of their refs.
type RefusedStepsError struct {
	Steps []CarriedStep
}

func (e *RefusedStepsError) Error() string {
	messages := make([]string, 0, len(e.Steps))
	for _, step := range e.Steps {
		messages = append(messages, step.Message())
	}
	return strings.Join(messages, "; ")
}

// Allowed reports whether the caller consents to reuse step, by name,
// although it changed since its run.
func (p *SelectedStepsPlan) Allowed(step string) bool {
	_, ok := p.allowed[step]
	return ok || p.allowAll
}

// Refusal returns the carried steps that the run cannot take outputs from,
// or nil when it can take all of them: a step changed since its run is taken
// only with the caller's consent.
func (p *SelectedStepsPlan) Refusal() *RefusedStepsError {
	var unavailable, changed []CarriedStep
	for _, step := range p.Carried {
		switch {
		case step.Unavailable != "":
			unavailable = append(unavailable, step)
		case step.Changed() && !p.Allowed(step.Step):
			changed = append(changed, step)
		}
	}
	if len(unavailable) == 0 && len(changed) == 0 {
		return nil
	}
	byRef := func(a, b CarriedStep) int { return cmp.Compare(a.Ref, b.Ref) }
	slices.SortStableFunc(unavailable, byRef)
	slices.SortStableFunc(changed, byRef)
	return &RefusedStepsError{Steps: append(unavailable, changed...)}
}

// PlanSelectedSteps plans a run of dag that executes only the selected steps
// of sel, as SelectedStepNodes seeds it, and works out what changed for each
// step carried from a run since that run. dag has its run's parameters
// resolved, and profile is the runtime profile of the run. An entry of
// outputsFromRuns whose run cannot give the step is reported in Carried
// instead of failing the plan; a request that names an unknown step, a step
// twice, or a selected step still fails it.
func PlanSelectedSteps(dag *ir.DAG, profile string, sel StepSelection) (*SelectedStepsPlan, error) {
	selected, err := selectedStepNames(dag, sel.Steps)
	if err != nil {
		return nil, err
	}
	refs := slices.Collect(maps.Keys(sel.FromRuns))
	for _, entry := range sel.Unavailable {
		refs = append(refs, entry.Ref)
	}
	slices.Sort(refs)
	if err := checkRunStepRefs(dag, selected, refs); err != nil {
		return nil, err
	}
	allowed, err := stepRefsByName(dag, sel.AllowChanged, false)
	if err != nil {
		return nil, fmt.Errorf("allowChangedSteps: %w", err)
	}
	usable, unavailable, err := usableRunStepSources(dag, sel.FromRuns)
	if err != nil {
		return nil, err
	}
	for _, entry := range sel.Unavailable {
		if entry.Step, err = resolveStepName(dag, entry.Ref); err != nil {
			return nil, err
		}
		unavailable = append(unavailable, entry)
	}
	nodes, reused, err := SelectedStepNodes(dag, sel.Steps, sel.Source, usable, sel.Outputs, sel.Sources)
	if err != nil {
		return nil, err
	}
	carried, err := carriedSteps(dag, profile, reused, sel, usable)
	if err != nil {
		return nil, err
	}
	carried = append(carried, unavailable...)
	order := make(map[string]int, len(dag.Steps))
	for i, step := range dag.Steps {
		order[step.Name] = i
	}
	slices.SortStableFunc(carried, func(a, b CarriedStep) int { return cmp.Compare(order[a.Step], order[b.Step]) })
	missing, err := missingSteps(dag, selected, usable, sel.Outputs, reused, unavailable)
	if err != nil {
		return nil, err
	}
	plan := &SelectedStepsPlan{Nodes: nodes, Reused: reused, Carried: carried, Missing: missing, allowAll: sel.AllowAllChanged}
	plan.allowed = make(map[string]struct{}, len(allowed))
	for name := range allowed {
		plan.allowed[name] = struct{}{}
	}
	plan.recordConsent()
	return plan, nil
}

// recordConsent writes onto Reused what changed for each carried step that
// the caller consents to reuse anyway, so that the run keeps the record and
// a later reuse of it sees that the outputs came from a changed step.
func (p *SelectedStepsPlan) recordConsent() {
	for _, step := range p.Carried {
		if !step.Changed() || !p.Allowed(step.Step) {
			continue
		}
		for i := range p.Reused {
			input := &p.Reused[i]
			if input.Step == step.Step && input.Via == ir.ReusedInputViaRun && input.RunID == step.DAGRunID {
				input.Changes = slices.Clone(step.Changes)
			}
		}
	}
}

// carriedSteps works out what changed for each step of reused that carries
// outputs from a run since that run.
func carriedSteps(dag *ir.DAG, profile string, reused []ir.ReusedInput, sel StepSelection, fromRuns RunStepSources) ([]CarriedStep, error) {
	refOf := make(map[string]string, len(fromRuns))
	runOf := make(map[string]*ir.DAGRunStatus, len(fromRuns))
	for ref, run := range fromRuns {
		name, err := resolveStepName(dag, ref)
		if err != nil {
			return nil, err
		}
		refOf[name], runOf[name] = ref, run
	}
	var current *fingerprint
	// earlier holds the fingerprint of each run, nil when its definition
	// cannot be compared, and tainted the steps of each run that took outputs
	// of a changed step there.
	earlier := make(map[string]*fingerprint)
	tainted := make(map[string]map[string]bool)
	var carried []CarriedStep
	for _, input := range reused {
		if input.Via != ir.ReusedInputViaRun {
			continue
		}
		run, ref := sel.Source, input.Step
		if own, ok := runOf[input.Step]; ok {
			run, ref = own, refOf[input.Step]
		}
		if current == nil {
			var err error
			if current, err = newFingerprint(dag, strings.Join(dag.Params, " "), profile); err != nil {
				return nil, err
			}
		}
		then, ok := earlier[run.DAGRunID]
		if !ok {
			then = runFingerprint(run, definitionOf(run, sel))
			earlier[run.DAGRunID] = then
			tainted[run.DAGRunID] = taintedSteps(dag, run)
		}
		var changes []ir.StepChange
		if then != nil {
			changes = current.changes(then, input.Step)
		}
		if tainted[run.DAGRunID][input.Step] {
			changes = append(changes, ir.StepChangeCarried)
		}
		if then == nil {
			changes = append(changes, ir.StepChangeUnknown)
		}
		carried = append(carried, CarriedStep{
			Step:       input.Step,
			Ref:        ref,
			DAGRunID:   run.DAGRunID,
			Changes:    changes,
			StartedAt:  input.StartedAt,
			FinishedAt: input.FinishedAt,
		})
	}
	return carried, nil
}

// definitionOf returns the definition that run ran, nil when it is unknown.
func definitionOf(run *ir.DAGRunStatus, sel StepSelection) *ir.DAG {
	if definition, ok := sel.Definitions[run.DAGRunID]; ok {
		return definition
	}
	if sel.Source != nil && run.DAGRunID == sel.Source.DAGRunID {
		return sel.SourceDefinition
	}
	return nil
}

// runFingerprint returns the fingerprint of the definition that run ran, nil
// when it cannot be compared.
func runFingerprint(run *ir.DAGRunStatus, definition *ir.DAG) *fingerprint {
	if definition == nil {
		return nil
	}
	then, err := newFingerprint(definition, run.Params, run.ProfileName)
	if err != nil {
		return nil
	}
	return then
}

// missingSteps lists, in the order of the DAG, the steps that a selected step
// depends on and that neither a run nor the caller gives outputs.
func missingSteps(dag *ir.DAG, selected map[string]struct{}, fromRuns RunStepSources, outputs map[string]map[string]string, reused []ir.ReusedInput, unavailable []CarriedStep) ([]string, error) {
	skipped := make([]string, 0, len(dag.Steps))
	for _, step := range dag.Steps {
		if _, ok := selected[step.Name]; !ok {
			skipped = append(skipped, step.Name)
		}
	}
	needed, err := carryingSteps(dag, selected, skipped, fromRuns, outputs)
	if err != nil {
		return nil, err
	}
	given, err := stepRefsByName(dag, slices.Sorted(maps.Keys(outputs)), false)
	if err != nil {
		return nil, err
	}
	for _, input := range reused {
		given[input.Step] = input.Step
	}
	for _, entry := range unavailable {
		given[entry.Step] = entry.Ref
	}
	var missing []string
	for _, name := range skipped {
		if _, need := needed[name]; !need {
			continue
		}
		if _, ok := given[name]; !ok {
			missing = append(missing, name)
		}
	}
	return missing, nil
}
