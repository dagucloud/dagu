// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagtest

import (
	"cmp"
	"encoding/json"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

// Handling is what one test case supplies for the sources the fail-closed
// rule checks.
type Handling struct {
	// Mocks maps a step key, a step's name or id or handler_on.<event> for a
	// lifecycle handler, to the fields its mock sets.
	Mocks map[string]MockFields
	// DefaultMock is the case's default mock, or nil when it has none. It
	// counts as the mock of every step and handler with a source that Mocks
	// and Real do not name.
	DefaultMock *MockFields
	// Real lists the step keys of the steps that run their real action.
	Real []string
	// Precondition reports whether the case sets precondition, the result of
	// the DAG-level preconditions that run a command.
	Precondition bool
	// Params lists the names of the params the case gives a value.
	Params []string
	// Secrets lists the DAG secret names and SSH credential refs the case
	// gives a value. A case with a real step needs a value for each.
	Secrets []string
}

// MockFields reports which fields a step's mock sets.
type MockFields struct {
	// Results holds one entry for each result the mock scripts. An inline
	// mock has one, and a mock that sets no result field, such as {}, has
	// none.
	Results []ResultFields
	// Precondition reports whether the mock sets precondition.
	Precondition bool
	// Review reports whether the mock sets review.
	Review bool
}

// ResultFields reports which fields one result of a mock sets.
type ResultFields struct {
	// Condition reports whether the result sets condition.
	Condition bool
}

// Finding is one source that makes a case invalid.
type Finding struct {
	// Source is the label Spec 101 gives the source, such as
	// "param build_date", "precondition", "step deploy",
	// "step fanout, body step upload", or "handler_on.failure".
	Source string
	// Message states what the source does and how the case handles it.
	Message string
}

// String returns the finding as "<source>: <message>", or as its message
// when it has no source.
func (f Finding) String() string {
	if f.Source == "" {
		return f.Message
	}
	return f.Source + ": " + f.Message
}

// CheckFailClosed returns every source of dag that would run a command, read
// a file, wait for values from a person, or repeat without end, and that
// handling leaves unhandled, as the fail-closed rule of Spec 101 defines. It
// covers every step and lifecycle handler, whether or not a run reaches it.
// Findings come in the order Spec 101 lists them: params, secrets in a case
// with a real step, the DAG-level preconditions, steps in DAG order, then
// handlers, and the sources of one step in the order of the rule's table. A
// case with no finding may run.
func CheckFailClosed(dag *ir.DAG, handling Handling) []Finding {
	c := checker{
		mocks:       handling.Mocks,
		defaultMock: handling.DefaultMock,
		real:        toSet(handling.Real),
		params:      toSet(handling.Params),
	}
	c.checkParams(dag.ParamDefs)
	if len(handling.Real) > 0 {
		c.checkSecrets(dag, toSet(handling.Secrets))
	}
	if !handling.Precondition && slices.ContainsFunc(dag.Preconditions, (*ir.Condition).RunsCommand) {
		c.add("precondition", "command check or substituting eval runs a command; set precondition in the case")
	}
	for _, step := range dag.Steps {
		c.checkStep("step "+stepLabel(step), step, stepKeys(step)...)
	}
	for _, h := range lifecycleHandlers(dag.HandlerOn) {
		c.checkStep(h.key, h.step, h.key)
	}
	return c.findings
}

const (
	fixMockOrReal = "mock it or list it under real"
	// handlerKeyPrefix starts every key that names a lifecycle handler.
	handlerKeyPrefix = "handler_on."
)

type checker struct {
	mocks       map[string]MockFields
	defaultMock *MockFields
	real        map[string]struct{}
	params      map[string]struct{}
	findings    []Finding
}

func (c *checker) add(source, message string) {
	c.findings = append(c.findings, Finding{Source: source, Message: message})
}

func (c *checker) checkParams(defs []ir.ParamDef) {
	for _, def := range defs {
		if !hasCommandSubstitution(def.Eval) {
			continue
		}
		if _, ok := c.params[def.Name]; ok {
			continue
		}
		c.add("param "+def.Name, "eval runs a command; give it a value under params")
	}
}

// checkSecrets reports each DAG secret and SSH credential ref without a
// value, since a real step may read any of them. Refs come in lexicographic
// order.
func (c *checker) checkSecrets(dag *ir.DAG, values map[string]struct{}) {
	for _, secret := range dag.Secrets {
		if _, ok := values[secret.Name]; !ok {
			c.add("secret "+secret.Name, "a real step may read it; give it a value under secrets")
		}
	}
	refs := make([]string, 0, len(dag.CredentialRefs))
	for _, ref := range dag.CredentialRefs {
		refs = append(refs, ref.Ref)
	}
	slices.Sort(refs)
	for _, ref := range slices.Compact(refs) {
		if _, ok := values[ref]; !ok {
			c.add("secret "+ref, "a real step may read this SSH credential; give it a value under secrets")
		}
	}
}

// checkStep reports the sources of a step or handler that its mock and real,
// looked up by keys, leave unhandled.
func (c *checker) checkStep(label string, step ir.Step, keys ...string) {
	mock, mocked := c.lookupMock(keys)
	real := c.isReal(keys)
	// Spec 101 narrows which steps take the default mock and when its
	// results replace a side-effect-free action. No narrowing changes a
	// finding, so it applies to every step and handler the case does not
	// name.
	if !mocked && !real && c.defaultMock != nil {
		mock, mocked = *c.defaultMock, true
	}
	replaced := replacesAction(step, mocked, mock, real)
	for _, o := range stepOffenses(step, mock, replaced, real) {
		c.add(label+o.label, o.subject+" "+o.does+"; "+o.fix)
	}
}

func (c *checker) lookupMock(keys []string) (MockFields, bool) {
	for _, key := range keys {
		if mock, ok := c.mocks[key]; ok {
			return mock, true
		}
	}
	return MockFields{}, false
}

func (c *checker) isReal(keys []string) bool {
	return slices.ContainsFunc(keys, func(key string) bool {
		_, ok := c.real[key]
		return ok
	})
}

// stepKeys returns the keys a case may name a step by: its name and its id.
// A key that starts with handler_on. always names a handler, so a step name
// of that form is no key.
func stepKeys(step ir.Step) []string {
	var keys []string
	if !strings.HasPrefix(step.Name, handlerKeyPrefix) {
		keys = append(keys, step.Name)
	}
	if step.ID != "" && step.ID != step.Name {
		keys = append(keys, step.ID)
	}
	return keys
}

// stepLabel names a step in a finding: its id, or its name when it has none.
func stepLabel(step ir.Step) string {
	if step.ID != "" {
		return step.ID
	}
	return step.Name
}

// replacesAction reports whether a mock replaces the step's action: it sets a
// result field, or the action is neither side-effect free nor real.
func replacesAction(step ir.Step, mocked bool, mock MockFields, real bool) bool {
	return mocked && (len(mock.Results) > 0 || !real && !sideEffectFree(step))
}

// sourceKind orders the sources of one step as the fail-closed table lists
// them.
type sourceKind int

const (
	sourceAction sourceKind = iota
	sourcePrecondition
	sourceRepeatCondition
	sourceOutputFile
	sourceContinuous
	sourceForeachBody
	sourceWait
)

// offense is one unhandled source of a step: what it is, what it does, and
// how a case handles it. label extends the step's label for a body step.
type offense struct {
	kind    sourceKind
	label   string
	subject string
	does    string
	fix     string
}

// stepOffenses returns the sources of a step, its foreach body included,
// that its mock and real leave unhandled.
func stepOffenses(step ir.Step, mock MockFields, replaced, real bool) []offense {
	offenses := ownOffenses(step, mock, replaced, real)
	if step.Foreach != nil && !replaced {
		offenses = append(offenses, bodyOffenses(step)...)
	}
	slices.SortStableFunc(offenses, func(a, b offense) int { return cmp.Compare(a.kind, b.kind) })
	return offenses
}

// bodyOffenses returns the sources of a foreach step's body. A body step
// takes no mock and no real, so only a mock of the foreach step handles them.
func bodyOffenses(step ir.Step) []offense {
	var offenses []offense
	for _, body := range flattenBody(step.Foreach.Steps) {
		for _, o := range ownOffenses(body, MockFields{}, false, false) {
			o.kind = sourceForeachBody
			o.label = ", body step " + stepLabel(body)
			o.fix = "mock the whole foreach step"
			offenses = append(offenses, o)
		}
	}
	return offenses
}

// ownOffenses returns the sources of one step, not counting its foreach
// body, that its mock and real leave unhandled.
func ownOffenses(step ir.Step, mock MockFields, replaced, real bool) []offense {
	// The step's own action runs without the case naming it.
	runsUnnamed := !replaced && !real

	var offenses []offense
	add := func(kind sourceKind, subject, does, fix string) {
		offenses = append(offenses, offense{kind: kind, subject: subject, does: does, fix: fix})
	}
	// A human task runs no action, and a foreach body is checked step by step.
	if step.HumanTask == nil && step.Foreach == nil && !sideEffectFree(step) && runsUnnamed {
		add(sourceAction, describeAction(step), "has side effects", fixMockOrReal)
	}
	if !real && !mock.Precondition && slices.ContainsFunc(step.Preconditions, (*ir.Condition).RunsCommand) {
		add(sourcePrecondition, "precondition", "runs a command", "set precondition in its mock or list it under real")
	}
	if !real && step.RepeatPolicy.Condition.RunsCommand() && !scriptsEveryCondition(mock) {
		add(sourceRepeatCondition, "repeat condition", "runs a command",
			"set condition in every result of its mock or list it under real")
	}
	if runsUnnamed && readsOutputFile(step) {
		add(sourceOutputFile, "object-form output", "reads a file", fixMockOrReal)
	}
	if runsUnnamed && isContinuous(step) && sideEffectFree(step) {
		add(sourceContinuous, "repeat policy", "has no limit", "mock it with a result or list it under real")
	}
	if waiting := waitNeedingValues(step); waiting != "" && !mock.Review {
		add(sourceWait, waiting, "waits for required values", "set review in its mock")
	}
	return offenses
}

// sideEffectFree reports whether the step's action may run for real in a
// test without the case naming it.
func sideEffectFree(step ir.Step) bool {
	switch {
	case step.Nodes != nil:
		// A step with nodes starts a child run on each matched inventory node.
		return false
	case step.Foreach != nil:
		return len(bodyOffenses(step)) == 0
	default:
		return registry.ExecutorCapabilitiesFor(step.ExecutorConfig.Type).SideEffectFree
	}
}

// hasCommandSubstitution reports whether an eval holds backtick or $()
// command substitution, the only part of an eval that runs a command
// (Spec 011). Escaped and unclosed forms count too, so the check errs toward
// asking the test for a value.
func hasCommandSubstitution(eval string) bool {
	return strings.Contains(eval, "`") || strings.Contains(eval, "$(")
}

func scriptsEveryCondition(mock MockFields) bool {
	return len(mock.Results) > 0 && !slices.ContainsFunc(mock.Results, func(r ResultFields) bool {
		return !r.Condition
	})
}

func readsOutputFile(step ir.Step) bool {
	for _, entry := range step.StructuredOutput {
		if entry.From == ir.StepOutputSourceFile {
			return true
		}
	}
	return false
}

// waitNeedingValues names what makes the step wait for values from a
// person, or returns "" when its wait completes with no values, as Spec 101
// defines a wait that needs values.
func waitNeedingValues(step ir.Step) string {
	switch {
	case step.HumanTask != nil && formNeedsValues(step.HumanTask.Form):
		return "human.task"
	case step.Approval != nil && len(step.Approval.Required) > 0:
		return "approval"
	default:
		return ""
	}
}

// formNeedsValues reports whether completing a human.task with no values
// fails, as the completion path validates its input.
func formNeedsValues(form json.RawMessage) bool {
	_, err := spec.ValidateHumanTaskInputs(form, nil, false)
	return err != nil
}

// isContinuous reports whether the step repeats with no limit (Spec 083). A
// limit that resolves at run time counts as a limit, as Spec 101 defines a
// continuous step.
func isContinuous(step ir.Step) bool {
	rp := step.RepeatPolicy
	return rp.RepeatMode != "" && rp.Limit <= 0 && rp.LimitStr == ""
}

// flattenBody returns the steps of a foreach body, each nested foreach step
// followed by its own body.
func flattenBody(steps []ir.Step) []ir.Step {
	var flat []ir.Step
	for _, step := range steps {
		flat = append(flat, step)
		if step.Foreach != nil {
			flat = append(flat, flattenBody(step.Foreach.Steps)...)
		}
	}
	return flat
}

// lifecycleHandler is a handler the DAG defines, with the key a case names it
// by.
type lifecycleHandler struct {
	key  string
	kind ir.HandlerType
	step ir.Step
}

func lifecycleHandlers(on ir.HandlerOn) []lifecycleHandler {
	events := []struct {
		name string
		kind ir.HandlerType
		step *ir.Step
	}{
		{"init", ir.HandlerOnInit, on.Init},
		{"success", ir.HandlerOnSuccess, on.Success},
		{"failure", ir.HandlerOnFailure, on.Failure},
		{"abort", ir.HandlerOnAbort, on.Abort},
		{"exit", ir.HandlerOnExit, on.Exit},
		{"wait", ir.HandlerOnWait, on.Wait},
	}
	var handlers []lifecycleHandler
	for _, event := range events {
		if event.step != nil {
			handlers = append(handlers, lifecycleHandler{key: handlerKeyPrefix + event.name, kind: event.kind, step: *event.step})
		}
	}
	return handlers
}

func toSet(names []string) map[string]struct{} {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}
	return set
}
