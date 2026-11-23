// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagtest

import (
	"regexp"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

// validate reports the case fields that the tested DAG rejects.
func (b *caseBuilder) validate(c *Case, file fileSpec) {
	for _, e := range file.secrets {
		known := slices.ContainsFunc(b.dag.Secrets, func(s secretref.Ref) bool { return s.Name == e.name }) ||
			slices.ContainsFunc(b.dag.CredentialRefs, func(s secretref.Ref) bool { return s.Ref == e.name })
		if !known {
			b.fail("secrets."+e.name, "names no DAG secret or SSH credential ref")
		}
	}
	if c.Precondition != nil && !slices.ContainsFunc(b.dag.Preconditions, runsCommand) {
		b.fail("precondition", "the DAG has no precondition that runs a command")
	}
}

// checkReal reports a step that real cannot take.
func (b *caseBuilder) checkReal(label, key string, t *stepTarget) {
	if t.step.Foreach != nil {
		b.fail(label, "%q is a foreach step, which cannot run for real; mock it", key)
	}
}

// checkShape reports a mock that mixes results with inline result fields,
// or sets an empty results.
func (b *caseBuilder) checkShape(label string, m mockSpec) {
	if m.inline != nil && m.results != nil {
		b.fail(label, "sets results together with inline result fields")
	}
	if m.results != nil && len(m.results) == 0 {
		b.fail(label+".results", "must hold at least one result")
	}
}

// checkMock reports the fields of a mock under mocks that the step it names
// rejects (Spec 101, Invalid Mocks).
func (b *caseBuilder) checkMock(label string, step *ir.Step, m mockSpec, results []labeledResult, real bool) {
	b.checkShape(label, m)
	switch {
	case len(results) > 0 && real:
		b.fail(label, "sets a result field on a step that runs for real; set only precondition and review")
	case len(results) > 0 && step.HumanTask != nil:
		b.fail(label, "sets a result field on a human.task step, which has no action to replace")
	}
	if m.precondition != nil && !slices.ContainsFunc(step.Preconditions, runsCommand) {
		b.fail(label+".precondition", "the step has no precondition that runs a command")
	}
	for _, r := range results {
		if r.spec.condition != nil && !runsCommand(step.RepeatPolicy.Condition) {
			b.fail(r.label+".condition", "the step has no repeat condition that runs a command")
		}
	}
	if m.review != nil && step.HumanTask == nil && step.Approval == nil {
		b.fail(label+".review", "the step does not wait for a person")
	}
	if !real && step.HumanTask == nil && mockReplaces(step, len(results) > 0, real) {
		b.checkOutputs(label, *step, results)
	}
}

// checkReview reports a review that the wait it settles rejects.
func (b *caseBuilder) checkReview(label string, step *ir.Step, r *reviewSpec, review *Review) {
	values := label + ".values"
	switch {
	case step.HumanTask != nil && review.Reject:
		b.fail(label+".decision", "a human.task cannot be rejected; approve it with values")
	case step.HumanTask != nil:
		if _, err := spec.ValidateHumanTaskInputs(step.HumanTask.Form, review.Values, false); err != nil {
			b.fail(values, "the task's form rejects them: %v", err)
		}
	case step.Approval != nil:
		for _, v := range r.values {
			if !v.scalar {
				b.fail(values+"."+v.name, "must be a scalar")
			}
		}
		if review.Reject {
			return
		}
		var missing []string
		for _, name := range step.Approval.Required {
			if text, _ := review.Values[name].(string); strings.TrimSpace(text) == "" {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			b.fail(values, "leaves out required inputs %s", strings.Join(missing, ", "))
		}
	}
}

// outputNamePattern is the form of an output name on a step with runtime
// outputs.
var outputNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// checkOutputs checks the outputs of the results of a mock that replaces a
// step's action. With no results, the step takes one successful result with
// no output.
func (b *caseBuilder) checkOutputs(label string, step ir.Step, results []labeledResult) {
	if len(results) == 0 {
		results = []labeledResult{{label: label}}
	}
	known, runtime := stepOutputs(step)
	for _, r := range results {
		path, outputs := r.label+".outputs", r.spec.outputs
		switch {
		case outputs != nil && step.ID == "":
			b.fail(path, "the step has no id, so no reference can read its outputs; give the step an id")
		case runtime:
			for name := range outputs {
				if !outputNamePattern.MatchString(name) {
					b.fail(path+"."+name, "is not a valid output name")
				}
			}
		case r.spec.exitCode == 0 && step.ID != "":
			// A step without an id cannot set outputs, so it is not asked
			// for its known ones either.
			b.checkKnownOutputs(path, known, outputs)
		}
	}
}

// checkKnownOutputs checks that a successful result sets every known output
// of a step and no other name, each with a value its type takes.
func (b *caseBuilder) checkKnownOutputs(path string, known map[string]ir.StepOutputDeclaration, outputs map[string]any) {
	var missing []string
	for name := range known {
		if _, ok := outputs[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		b.fail(path, "leaves out declared outputs %s", strings.Join(missing, ", "))
	}
	for name, value := range outputs {
		decl, ok := known[name]
		_, isString := value.(string)
		switch {
		case !ok:
			b.fail(path+"."+name, "is not an output the step declares")
		case decl.Source == "" && decl.Type != ir.StepDeclaredOutputTypeJSON && !isString:
			b.fail(path+"."+name, "must be a string, as the step declares it with type string")
		}
	}
}

// checkOutputAssertions reports an output assertion on a name that a step
// with known outputs does not declare.
func (b *caseBuilder) checkOutputAssertions(label string, step ir.Step, outputs []OutputExpect) {
	known, runtime := stepOutputs(step)
	if runtime || len(known) == 0 {
		return
	}
	for _, o := range outputs {
		if _, ok := known[o.Name]; !ok {
			b.fail(label+".outputs."+o.Name, "is not an output the step declares")
		}
	}
}

// stepOutputs returns a step's known outputs by name, and reports whether it
// has runtime outputs, whose names only a run reveals (Spec 101).
func stepOutputs(step ir.Step) (map[string]ir.StepOutputDeclaration, bool) {
	decls, runtime := spec.StepOutputs(step)
	if step.ExecutorConfig.Type == ir.ExecutorTypeDAGEnqueue || step.Parallel != nil || step.Nodes != nil {
		// A real run publishes the outputs of these children as a list, not
		// by name.
		runtime = true
	}
	known := make(map[string]ir.StepOutputDeclaration, len(decls))
	for _, decl := range decls {
		if _, ok := known[decl.Name]; !ok {
			known[decl.Name] = decl
		}
	}
	return known, runtime
}
