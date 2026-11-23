// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagtest

import (
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

// caseSources holds the merged fields of a case that validate checks against
// the tested DAG.
type caseSources struct {
	file        fileSpec
	params      []entry
	mocks       []keyedMock
	real        []*stepTarget
	defaultMock *mockSpec
}

// validate reports the case fields the tested DAG rejects, applies the
// default mock, and returns the sources the case leaves unhandled under the
// fail-closed rule.
func (b *caseBuilder) validate(c *Case, src caseSources) []Finding {
	for _, e := range src.file.secrets {
		known := slices.ContainsFunc(b.dag.Secrets, func(s secretref.Ref) bool { return s.Name == e.name }) ||
			slices.ContainsFunc(b.dag.CredentialRefs, func(s secretref.Ref) bool { return s.Ref == e.name })
		if !known {
			b.fail("secrets."+e.name, "names no DAG secret or SSH credential ref")
		}
	}
	if c.Precondition != nil && !slices.ContainsFunc(b.dag.Preconditions, (*ir.Condition).RunsCommand) {
		b.fail("precondition", "the DAG has no precondition that runs a command")
	}

	handling := Handling{
		Mocks:        make(map[string]MockFields, len(src.mocks)),
		Precondition: c.Precondition != nil,
		Secrets:      slices.Collect(maps.Keys(c.Secrets)),
	}
	for _, e := range src.params {
		handling.Params = append(handling.Params, e.name)
	}
	for _, t := range src.real {
		handling.Real = append(handling.Real, t.label)
	}
	for _, m := range src.mocks {
		handling.Mocks[m.target.label] = mockFields(m.mock)
	}
	if src.defaultMock != nil {
		fields := mockFields(*src.defaultMock)
		handling.DefaultMock = &fields
		b.applyDefaultMock(c, *src.defaultMock, src)
	}
	check := checkFailClosed(b.dag, handling)
	b.buildFix(c, check.sources, src.mocks)
	return check.findings
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
	if m.precondition != nil && !slices.ContainsFunc(step.Preconditions, (*ir.Condition).RunsCommand) {
		b.fail(label+".precondition", "the step has no precondition that runs a command")
	}
	for _, r := range results {
		if r.spec.condition != nil && !step.RepeatPolicy.Condition.RunsCommand() {
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

// applyDefaultMock validates the default mock and sets it as the script of
// each step and handler that needs handling and that mocks and real do not
// name.
func (b *caseBuilder) applyDefaultMock(c *Case, m mockSpec, src caseSources) {
	b.checkShape("default_mock", m)
	results := b.results("default_mock", m)
	for _, r := range results {
		if r.spec.outputs != nil {
			b.fail(r.label+".outputs", "the default mock sets no outputs; mock the step under mocks to set them")
		}
	}
	// Its values are checked once here, and its review again against each
	// waiting step it settles.
	b.met("default_mock.precondition", m.precondition)
	b.review("default_mock.review", &ir.Step{}, m.review)
	for _, t := range b.steps.all {
		mocked := slices.ContainsFunc(src.mocks, func(m keyedMock) bool { return m.target == t })
		if mocked || slices.Contains(src.real, t) {
			continue
		}
		if s, ok := b.defaultScript(t, m, results); ok {
			c.setScript(t, s)
			c.DefaultMocked = append(c.DefaultMocked, t.label)
		}
	}
}

// defaultScript returns how the default mock handles a step that needs
// handling, with only the fields that handle one of its sources. It reports
// false for a step with no source, which runs as it would without a mock.
func (b *caseBuilder) defaultScript(t *stepTarget, m mockSpec, results []labeledResult) (Script, bool) {
	step := *t.step
	if len(stepOffenses(step, MockFields{}, false, false)) == 0 {
		return Script{}, false
	}
	s := Script{DefaultMock: true}
	repeatCommand := step.RepeatPolicy.Condition.RunsCommand()
	// On a side-effect-free action, results replace it only where a replaced
	// action handles a source.
	resultSource := isContinuous(step) || readsOutputFile(step) || repeatCommand
	if step.HumanTask == nil && (!sideEffectFree(step) || len(results) > 0 && resultSource) {
		s.ReplacesAction = true
		s.Results = defaultOutputs(step, resultsOf(results))
		if !repeatCommand {
			for i := range s.Results {
				s.Results[i].Condition = nil
			}
		}
	}
	if slices.ContainsFunc(step.Preconditions, (*ir.Condition).RunsCommand) {
		s.Precondition = b.met("default_mock.precondition", m.precondition)
	}
	if waitNeedingValues(step) != "" {
		s.Review = b.review("default_mock.review", &step, m.review)
	}
	return s, true
}

// defaultOutputs makes each successful result of the default mock publish
// every known output of the step, as an empty string or as null for a json
// output. A step with runtime outputs publishes none.
func defaultOutputs(step ir.Step, results []Result) []Result {
	known, runtime := stepOutputs(step)
	if runtime || len(known) == 0 {
		return results
	}
	if len(results) == 0 {
		results = []Result{{}}
	}
	for i := range results {
		if results[i].ExitCode != 0 {
			continue
		}
		results[i].Outputs = make(map[string]any, len(known))
		for name, decl := range known {
			results[i].Outputs[name] = ""
			if decl.Type == ir.StepDeclaredOutputTypeJSON {
				results[i].Outputs[name] = nil
			}
		}
	}
	return results
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
		case step.ID != "":
			// A step without an id cannot set outputs, so it is not asked
			// for its known ones either.
			b.checkKnownOutputs(path, known, outputs, r.spec.exitCode == 0)
		}
	}
}

// checkKnownOutputs checks the outputs a result sets on a step with known
// outputs. Each declared one takes a value its type takes, and a successful
// result sets every known output and no other name.
func (b *caseBuilder) checkKnownOutputs(path string, known map[string]ir.StepOutputDeclaration, outputs map[string]any, succeeds bool) {
	var missing []string
	for name := range known {
		if _, ok := outputs[name]; !ok && succeeds {
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
		case !ok && succeeds:
			b.fail(path+"."+name, "is not an output the step declares")
		case ok && decl.Source == "" && decl.Type != ir.StepDeclaredOutputTypeJSON && !isString:
			b.fail(path+"."+name, "must be a string, as the step declares it with type string")
		}
	}
}

// outputExpects returns the output assertions on a step, each marked when
// the step declares its output with type: json. An assertion on a name that
// a step with known outputs does not declare makes the case invalid.
func (b *caseBuilder) outputExpects(label string, step ir.Step, outputs []OutputExpect) []OutputExpect {
	known, runtime := stepOutputs(step)
	outputs = slices.Clone(outputs)
	for i, o := range outputs {
		decl, ok := known[o.Name]
		switch {
		case ok:
			outputs[i].JSON = decl.Type == ir.StepDeclaredOutputTypeJSON
		case !runtime && len(known) > 0:
			b.fail(label+".outputs."+o.Name, "is not an output the step declares")
		}
	}
	return outputs
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

// mockFields reports the fields of a mock to the fail-closed checker.
func mockFields(m mockSpec) MockFields {
	fields := MockFields{Precondition: m.precondition != nil, Review: m.review != nil}
	if m.inline != nil {
		fields.Results = append(fields.Results, ResultFields{Condition: m.inline.condition != nil})
	}
	for _, r := range m.results {
		fields.Results = append(fields.Results, ResultFields{Condition: r.condition != nil})
	}
	return fields
}
