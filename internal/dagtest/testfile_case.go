// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagtest

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

// stepTarget is a step or lifecycle handler of the tested DAG that a step key
// names.
type stepTarget struct {
	step *ir.Step
	// handler is the handler's type, or empty for a step.
	handler ir.HandlerType
	// label names the target in findings and in Handling.
	label string
}

// stepIndex finds the steps and handlers of the tested DAG by step key.
type stepIndex struct {
	byKey map[string]*stepTarget
	// all holds the steps in DAG order, then the handlers.
	all []*stepTarget
}

func indexSteps(dag *ir.DAG) *stepIndex {
	x := &stepIndex{byKey: make(map[string]*stepTarget)}
	for i := range dag.Steps {
		t := &stepTarget{step: &dag.Steps[i], label: stepLabel(dag.Steps[i])}
		x.all = append(x.all, t)
		for _, key := range stepKeys(dag.Steps[i]) {
			x.byKey[key] = t
		}
	}
	for _, h := range lifecycleHandlers(dag.HandlerOn) {
		t := &stepTarget{step: &h.step, handler: h.kind, label: h.key}
		x.all = append(x.all, t)
		x.byKey[h.key] = t
	}
	return x
}

// caseBuilder merges a file's fields into one case and validates the result.
type caseBuilder struct {
	dag   *ir.DAG
	steps *stepIndex
	// errs holds the field errors, labeled with their path in the merged
	// case.
	errs []Finding
}

func (b *caseBuilder) fail(label, format string, args ...any) {
	b.errs = append(b.errs, Finding{Source: label, Message: fmt.Sprintf(format, args...)})
}

// keyedMock is a mock under mocks and the step its key names.
type keyedMock struct {
	key    string
	mock   mockSpec
	target *stepTarget
}

func buildCase(dag *ir.DAG, steps *stepIndex, file fileSpec, cs caseSpec) Case {
	b := &caseBuilder{dag: dag, steps: steps}
	params := mergeEntries(file.params, cs.params)
	c := Case{
		Name:         cs.name,
		Params:       assignments(params),
		Env:          assignments(mergeEntries(file.env, cs.env)),
		Secrets:      make(map[string]string, len(file.secrets)),
		ScheduledAt:  cs.scheduledAt,
		Trigger:      b.trigger(cs.trigger),
		Precondition: b.met("precondition", cs.precondition),
		Steps:        make(map[string]Script),
		Handlers:     make(map[ir.HandlerType]Script),
	}
	for _, e := range file.secrets {
		c.Secrets[e.name] = e.value
	}
	mocks := b.mergeMocks(file.mocks, cs.mocks)
	real := b.mergeReal(file.real, cs.real)
	for _, t := range real {
		c.setScript(t, Script{Real: true})
	}
	for _, m := range mocks {
		c.setScript(m.target, b.mockScript(m, slices.Contains(real, m.target)))
	}
	c.Expect = b.expect(cs.expect)
	findings := b.validate(&c, caseSources{
		file:        file,
		params:      params,
		mocks:       mocks,
		real:        real,
		defaultMock: cmp.Or(cs.defaultMock, file.defaultMock),
	})

	slices.SortFunc(b.errs, func(x, y Finding) int {
		return cmp.Or(cmp.Compare(x.Source, y.Source), cmp.Compare(x.Message, y.Message))
	})
	c.Errors = append(slices.Compact(b.errs), findings...)
	return c
}

func (c *Case) setScript(t *stepTarget, s Script) {
	if t.handler != "" {
		c.Handlers[t.handler] = s
	} else {
		c.Steps[t.step.Name] = s
	}
}

// mergeEntries returns the file's entries with the case's merged over them
// by name, in the order the names first appear.
func mergeEntries(file, own []entry) []entry {
	merged := slices.Clone(file)
	for _, e := range own {
		if i := slices.IndexFunc(merged, func(m entry) bool { return m.name == e.name }); i >= 0 {
			merged[i] = e
		} else {
			merged = append(merged, e)
		}
	}
	return merged
}

func assignments(entries []entry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.name+"="+e.value)
	}
	return out
}

func (b *caseBuilder) trigger(value *string) ir.TriggerType {
	if value == nil {
		return ir.TriggerTypeManual
	}
	trigger := ir.ParseTriggerType(*value)
	if trigger == ir.TriggerTypeUnknown || trigger == ir.TriggerTypeSubDAG {
		b.fail("trigger", "%q is not a trigger type a case can set; use manual, scheduler, webhook, retry, catchup, restart, backfill, or file", *value)
	}
	return trigger
}

// met reads a met or not_met value.
func (b *caseBuilder) met(label string, value *string) *bool {
	if value == nil {
		return nil
	}
	var met bool
	switch *value {
	case "met":
		met = true
	case "not_met":
	default:
		b.fail(label, "%q must be met or not_met", *value)
	}
	return &met
}

// mergeMocks returns the case's mocks and the file's mocks of the steps the
// case does not mock.
func (b *caseBuilder) mergeMocks(file, own []mockEntry) []keyedMock {
	merged := b.resolveMocks(own)
	for _, m := range b.resolveMocks(file) {
		if !slices.ContainsFunc(merged, func(c keyedMock) bool { return c.target == m.target }) {
			merged = append(merged, m)
		}
	}
	return merged
}

// resolveMocks finds the step each key of one mocks map names.
func (b *caseBuilder) resolveMocks(entries []mockEntry) []keyedMock {
	var out []keyedMock
	for _, e := range entries {
		label := "mocks." + e.key
		t, ok := b.steps.byKey[e.key]
		if !ok {
			b.fail(label, "names no step or handler of the DAG")
			continue
		}
		if i := slices.IndexFunc(out, func(m keyedMock) bool { return m.target == t }); i >= 0 {
			b.fail(label, "names the same step as mocks.%s", out[i].key)
			continue
		}
		out = append(out, keyedMock{key: e.key, mock: e.mock, target: t})
	}
	return out
}

// mergeReal returns the union of the file's and the case's real. Its labels
// count the file's items, then the case's items the file does not name.
func (b *caseBuilder) mergeReal(file, own []string) []*stepTarget {
	var merged []*stepTarget
	index := 0
	add := func(keys []string, union []*stepTarget) {
		seen := make(map[*stepTarget]string)
		for _, key := range keys {
			t, ok := b.steps.byKey[key]
			if ok && slices.Contains(union, t) {
				continue
			}
			label := indexPath("real", index)
			index++
			switch {
			case !ok:
				b.fail(label, "%q names no step or handler of the DAG", key)
				continue
			case seen[t] != "":
				b.fail(label, "%q names the same step as %s", key, seen[t])
				continue
			}
			b.checkReal(label, key, t)
			seen[t] = label
			merged = append(merged, t)
		}
	}
	add(file, nil)
	add(own, slices.Clone(merged))
	return merged
}

// labeledResult is one result of a mock and the label of its fields.
type labeledResult struct {
	label  string
	spec   resultSpec
	result Result
}

// results returns a mock's results, an inline mock as one.
func (b *caseBuilder) results(label string, m mockSpec) []labeledResult {
	var out []labeledResult
	add := func(label string, r resultSpec) {
		out = append(out, labeledResult{label: label, spec: r, result: Result{
			ExitCode:  r.exitCode,
			Stdout:    r.stdout,
			Stderr:    r.stderr,
			Outputs:   r.outputs,
			Condition: b.met(label+".condition", r.condition),
		}})
	}
	if m.inline != nil {
		add(label, *m.inline)
	}
	for i, r := range m.results {
		add(indexPath(label+".results", i), r)
	}
	return out
}

// mockScript returns how a mock under mocks handles the step it names.
func (b *caseBuilder) mockScript(m keyedMock, real bool) Script {
	label, step := "mocks."+m.key, m.target.step
	results := b.results(label, m.mock)
	s := Script{
		Real:         real,
		Precondition: b.met(label+".precondition", m.mock.precondition),
		Review:       b.review(label+".review", step, m.mock.review),
	}
	s.ReplacesAction = mockReplaces(step, len(results) > 0, real)
	if s.ReplacesAction {
		s.Results = resultsOf(results)
	}
	b.checkMock(label, step, m.mock, results, real)
	return s
}

// mockReplaces reports whether a mock under mocks replaces the step's
// action: it sets a result field, or the step is not real and its action has
// side effects. A human.task has no action.
func mockReplaces(step *ir.Step, hasResults, real bool) bool {
	return hasResults || !real && step.HumanTask == nil && !sideEffectFree(*step)
}

func resultsOf(results []labeledResult) []Result {
	var out []Result
	for _, r := range results {
		out = append(out, r.result)
	}
	return out
}

// review reads a review of the step it settles. An approval input is the
// scalar's text.
func (b *caseBuilder) review(label string, step *ir.Step, r *reviewSpec) *Review {
	if r == nil {
		return nil
	}
	review := &Review{}
	switch r.decision {
	case "approve":
	case "reject":
		review.Reject = true
	default:
		b.fail(label+".decision", "%q must be approve or reject", r.decision)
	}
	if len(r.values) > 0 {
		review.Values = make(map[string]any, len(r.values))
	}
	for _, v := range r.values {
		review.Values[v.name] = v.value
		if step.Approval != nil && step.HumanTask == nil {
			review.Values[v.name] = v.text
		}
	}
	b.checkReview(label, step, r, review)
	return review
}

var (
	runStatuses = []string{"succeeded", "failed", "aborted", "partially_succeeded", "rejected"}
	// nodeStatuses adds the statuses only a step can end with.
	nodeStatuses      = append(slices.Clone(runStatuses), "skipped", "not_started")
	notificationKinds = []string{"mail_on.failure", "mail_on.success", "mail_on.wait", "mail_on_error"}
)

func (b *caseBuilder) expect(e expectSpec) Expect {
	if e.status == nil && e.attempts == nil && len(e.steps) == 0 && !e.hasNotifications {
		b.fail("expect", "holds no assertion")
	}
	out := Expect{
		Status:   b.token("expect.status", e.status, runStatuses),
		Attempts: b.attempts("expect.attempts", e.attempts),
	}
	seen := make(map[*stepTarget]string)
	for _, s := range e.steps {
		label := "expect.steps." + s.key
		t, ok := b.steps.byKey[s.key]
		switch {
		case !ok:
			b.fail(label, "names no step or handler of the DAG")
			continue
		case seen[t] != "":
			b.fail(label, "names the same step as expect.steps.%s", seen[t])
			continue
		case s.empty:
			b.fail(label, "holds no assertion")
		}
		seen[t] = s.key
		out.Steps = append(out.Steps, b.stepExpect(label, t, s))
	}
	if e.hasNotifications {
		out.Notifications = make([]Notification, 0, len(e.notifications))
	}
	for i, n := range e.notifications {
		out.Notifications = append(out.Notifications, b.notification(indexPath("expect.notifications", i), n))
	}
	return out
}

func (b *caseBuilder) stepExpect(label string, t *stepTarget, s stepExpectSpec) StepExpect {
	statusLabel := label + ".status"
	if s.token {
		statusLabel = label
	}
	out := StepExpect{
		Key:      s.key,
		Handler:  t.handler,
		Status:   b.token(statusLabel, s.status, nodeStatuses),
		Attempts: b.attempts(label+".attempts", s.attempts),
		Outputs:  b.outputExpects(label, *t.step, s.outputs),
		Stdout:   b.stream(label+".stdout", s.stdout),
		Stderr:   b.stream(label+".stderr", s.stderr),
	}
	if s.calledWith != nil {
		out.CalledWith = b.callExpect(label+".called_with", *t.step, *s.calledWith)
	}
	if t.handler == "" {
		out.Step = t.step.Name
	}
	return out
}

// callExpect reads a called_with assertion on step. run takes a step
// written with run, and with a step written with action. A step whose
// action never runs in the step itself takes neither, nor env.
func (b *caseBuilder) callExpect(label string, step ir.Step, c callSpec) *CallExpect {
	out := &CallExpect{With: c.with}
	switch {
	case c.run == nil && c.with == nil && c.env == nil:
		b.fail(label, "holds no assertion")
		return out
	case step.HumanTask != nil:
		b.fail(label, "a human.task step has no action to call")
		return out
	case step.Foreach != nil:
		b.fail(label, "a foreach step calls only its body steps, which have no step key")
		return out
	case step.Nodes != nil:
		b.fail(label, "a step with nodes resolves its command in the child run of each node; assert on the child DAG's steps")
		return out
	}
	if c.run != nil {
		out.Run = b.runExpect(label+".run", step, *c.run)
	}
	switch {
	case c.with == nil:
	case step.ActionWith == nil && hasRun(step):
		b.fail(label+".with", "the step runs a command, which takes no with; assert run")
	case step.ActionWith == nil:
		b.fail(label+".with", "the step is not written with action, so it takes no with")
	case len(c.with) == 0:
		b.fail(label+".with", "holds no assertion")
	}
	if c.env != nil && len(c.env) == 0 {
		b.fail(label+".env", "holds no assertion")
	}
	for _, e := range c.env {
		out.Env = append(out.Env, EnvExpect{Name: e.name, Value: e.value})
	}
	return out
}

// runExpect reads a run assertion on step.
func (b *caseBuilder) runExpect(label string, step ir.Step, r runSpec) *RunExpect {
	switch {
	case step.ActionWith != nil:
		b.fail(label, "the step runs action %s, which takes no run; assert with", spec.ActionName(&step))
	case !hasRun(step):
		b.fail(label, "the step has no command or script")
	}
	if r.stream == nil {
		return &RunExpect{Text: r.text}
	}
	if r.stream.contains == nil && r.stream.matches == nil {
		b.fail(label, "must set contains, matches, or both")
	}
	stream := b.stream(label, r.stream)
	return &RunExpect{Contains: stream.Contains, Matches: stream.Matches}
}

// hasRun reports whether a step runs a command or a script of its own.
func hasRun(step ir.Step) bool {
	return len(step.Commands) > 0 || step.Script != ""
}

func (b *caseBuilder) notification(label string, n notificationSpec) Notification {
	if !slices.Contains(notificationKinds, n.kind) {
		b.fail(label+".kind", "%q must be one of %s", n.kind, strings.Join(notificationKinds, ", "))
	}
	out := Notification{Kind: n.kind, To: n.to}
	if n.step == "" {
		return out
	}
	if n.kind != "mail_on_error" {
		b.fail(label+".step", "is valid only with kind mail_on_error")
	}
	if t, ok := b.steps.byKey[n.step]; ok {
		out.Step = t.step.Name
	} else {
		b.fail(label+".step", "%q names no step or handler of the DAG", n.step)
	}
	return out
}

func (b *caseBuilder) token(label string, value *string, allowed []string) string {
	if value == nil {
		return ""
	}
	if !slices.Contains(allowed, *value) {
		b.fail(label, "%q must be one of %s", *value, strings.Join(allowed, ", "))
	}
	return *value
}

func (b *caseBuilder) attempts(label string, value *int) *int {
	if value != nil && *value < 0 {
		b.fail(label, "must not be negative")
	}
	return value
}

func (b *caseBuilder) stream(label string, s *streamSpec) *StreamExpect {
	if s == nil {
		return nil
	}
	out := &StreamExpect{Contains: s.contains}
	if s.matches != nil {
		re, err := regexp.Compile(*s.matches)
		if err != nil {
			b.fail(label+".matches", "is not a valid RE2 expression: %v", err)
		}
		out.Matches = re
	}
	return out
}
