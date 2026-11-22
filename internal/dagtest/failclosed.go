// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagtest

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// Handling is what one test case supplies for the sources the fail-closed
// rule checks.
type Handling struct {
	// Mocks maps a step's name or id, or handler_on.<event> for a lifecycle
	// handler, to the fields its mock sets.
	Mocks map[string]MockFields
	// Real lists the steps that run their real action, named as in Mocks.
	Real []string
	// Precondition reports whether the case sets precondition, the result of
	// the DAG-level preconditions that run a command.
	Precondition bool
	// Params lists the names of the params the case gives a value.
	Params []string
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
	// Source names the param, the DAG-level preconditions, or the step or
	// handler, such as "param build_date", "step deploy", or
	// "handler_on.failure".
	Source string
	// Message states what the source does and how the case handles it.
	Message string
}

// String returns the finding as "<source>: <message>".
func (f Finding) String() string {
	return f.Source + ": " + f.Message
}

// CheckFailClosed returns every source of dag that would run a command, read
// a file, wait for a person, or repeat without end, and that handling leaves
// unhandled, as the fail-closed rule of workflow tests defines. It covers
// every step and lifecycle handler, whether or not a run reaches it. Findings
// come in a fixed order: params, the DAG-level preconditions, steps in DAG
// order, then handlers. A case with no finding may run.
func CheckFailClosed(dag *ir.DAG, handling Handling) []Finding {
	c := checker{
		mocks:  handling.Mocks,
		real:   toSet(handling.Real),
		params: toSet(handling.Params),
	}
	c.checkParams(dag.ParamDefs)
	if !handling.Precondition && slices.ContainsFunc(dag.Preconditions, runsCommand) {
		c.add("preconditions", "a precondition runs a command; set precondition in the case")
	}
	handlers := lifecycleHandlers(dag.HandlerOn)
	for _, step := range dag.Steps {
		name := "step " + step.Name
		// A case names a handler by its key, so a step of that name would
		// share the handler's mock and real.
		if slices.ContainsFunc(handlers, func(h lifecycleHandler) bool { return h.key == step.Name }) {
			c.add(name, "name is the key of a lifecycle handler; rename the step")
		}
		c.checkStep(name, step, stepKeys(step)...)
	}
	for _, h := range handlers {
		c.checkStep(h.key, h.step, h.key)
	}
	return c.findings
}

const fixMockOrReal = "mock it or list it under real"

type checker struct {
	mocks    map[string]MockFields
	real     map[string]struct{}
	params   map[string]struct{}
	findings []Finding
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

// checkStep reports the sources of a step or handler that its mock and real,
// looked up by keys, leave unhandled.
func (c *checker) checkStep(name string, step ir.Step, keys ...string) {
	mock, mocked := c.lookupMock(keys)
	real := c.isReal(keys)
	if real && len(mock.Results) > 0 {
		c.add(name, "mock sets a result field on a step under real; keep only precondition and review in its mock, or take it out of real")
	}
	replaced := replacesAction(step, mocked, mock, real)

	// A body step takes no mock and no real of its own, so only a mock that
	// replaces the whole foreach step handles its sources.
	if step.Foreach != nil && !replaced {
		for _, body := range flattenBody(step.Foreach.Steps) {
			for _, o := range stepOffenses(body, MockFields{}, false, false) {
				c.add(name, fmt.Sprintf("%s of body step %s %s; mock the whole foreach step", o.subject, body.Name, o.does))
			}
		}
	}
	for _, o := range stepOffenses(step, mock, replaced, real) {
		c.add(name, o.subject+" "+o.does+"; "+o.fix)
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

// stepKeys returns the names a case may give a step: its name and its id.
func stepKeys(step ir.Step) []string {
	if step.ID == "" || step.ID == step.Name {
		return []string{step.Name}
	}
	return []string{step.Name, step.ID}
}

// replacesAction reports whether a mock replaces the step's action: it sets a
// result field, or the action is neither side-effect free nor real.
func replacesAction(step ir.Step, mocked bool, mock MockFields, real bool) bool {
	return mocked && (len(mock.Results) > 0 || !real && !sideEffectFree(step))
}

// offense is one unhandled source of a step: what it is, what it does, and
// how a case handles it.
type offense struct {
	subject string
	does    string
	fix     string
}

// stepOffenses returns the sources of one step, not counting its foreach
// body, that its mock and real leave unhandled.
func stepOffenses(step ir.Step, mock MockFields, replaced, real bool) []offense {
	// The step's own action runs without the case naming it.
	runsUnnamed := !replaced && !real

	var offenses []offense
	actionFlagged := false
	// A human task runs no action, and a foreach body is checked step by step.
	if step.HumanTask == nil && step.Foreach == nil && !sideEffectFree(step) && runsUnnamed {
		offenses = append(offenses, offense{describeAction(step), "has side effects", fixMockOrReal})
		actionFlagged = true
	}
	if !real && !mock.Precondition && slices.ContainsFunc(step.Preconditions, runsCommand) {
		offenses = append(offenses, offense{"precondition", "runs a command",
			"set precondition in its mock or list it under real"})
	}
	if !real && runsCommand(step.RepeatPolicy.Condition) && !scriptsEveryCondition(mock) {
		offenses = append(offenses, offense{"repeat condition", "runs a command",
			"set condition in every result of its mock or list it under real"})
	}
	if runsUnnamed && readsOutputFile(step) {
		offenses = append(offenses, offense{"object-form output", "reads a file", fixMockOrReal})
	}
	if waiting := waitKind(step); waiting != "" && !mock.Review {
		offenses = append(offenses, offense{waiting, "waits for a person", "set review in its mock"})
	}
	if runsUnnamed && !actionFlagged && mayRepeatForever(step) {
		offenses = append(offenses, offense{"repeat policy", "has no fixed limit", "mock it with results"})
	}
	return offenses
}

// sideEffectFree reports whether the step's action may run for real in a
// test without the case naming it.
func sideEffectFree(step ir.Step) bool {
	// A step with nodes starts a child run on each matched inventory node.
	if step.Nodes != nil {
		return false
	}
	return registry.ExecutorCapabilitiesFor(step.ExecutorConfig.Type).SideEffectFree
}

// runsCommand reports whether a condition is a command check (Spec 023) or
// an eval with command substitution.
func runsCommand(cond *ir.Condition) bool {
	if cond == nil {
		return false
	}
	if cond.Eval != "" {
		return hasCommandSubstitution(cond.Eval)
	}
	return len(cond.ExpectedPatterns()) == 0
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

// waitKind names what makes the step wait for a person, or returns "" when
// it does not wait.
func waitKind(step ir.Step) string {
	switch {
	case step.HumanTask != nil:
		return "human.task"
	case step.Approval != nil:
		return "approval"
	default:
		return ""
	}
}

// mayRepeatForever reports whether the step repeats with no fixed limit: it
// has no limit, as a continuous step (Spec 083), or a limit that resolves at
// run time, where zero or less also means none.
func mayRepeatForever(step ir.Step) bool {
	rp := step.RepeatPolicy
	return rp.RepeatMode != "" && (rp.Limit <= 0 || rp.LimitStr != "")
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
	step ir.Step
}

func lifecycleHandlers(on ir.HandlerOn) []lifecycleHandler {
	events := []struct {
		name string
		step *ir.Step
	}{
		{"init", on.Init},
		{"success", on.Success},
		{"failure", on.Failure},
		{"abort", on.Abort},
		{"exit", on.Exit},
		{"wait", on.Wait},
	}
	var handlers []lifecycleHandler
	for _, event := range events {
		if event.step != nil {
			handlers = append(handlers, lifecycleHandler{key: "handler_on." + event.name, step: *event.step})
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
