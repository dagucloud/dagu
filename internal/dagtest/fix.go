// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagtest

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"gopkg.in/yaml.v3"
)

// Values the fix writes (Spec 101, The Fix).
const (
	fixMet            = "met"
	fixNotMet         = "not_met"
	fixApprove        = "approve"
	fixReject         = "reject"
	fixApprovalInput  = "TODO"
	fixReplaceComment = "replace with a value the form accepts"
)

// caseFix is the fix of an invalid case: the case fields and the test file
// fields that handle its unhandled sources.
type caseFix struct {
	params       []fixValue
	precondition bool
	// mocks holds one mock for each step or handler with a source, in the
	// order of the sources.
	mocks   []*fixMock
	secrets []string
}

// fixMock is a mock in the fix: the step's current mock with the fields its
// sources need added.
type fixMock struct {
	key          string
	target       *stepTarget
	inline       *fixResult
	results      []fixResult
	precondition string
	review       *fixReview
}

type fixResult struct {
	// exitCode is nil when the result leaves it at 0.
	exitCode  *int
	stdout    string
	stderr    string
	outputs   map[string]any
	condition string
}

type fixReview struct {
	decision string
	values   []fixValue
}

// fixValue is one named value of the fix.
type fixValue struct {
	name  string
	value any
	// replace marks a value that the form rejects, which the author replaces.
	replace bool
}

// buildFix sets the fix of c from the unhandled sources of the fail-closed
// rule.
func (b *caseBuilder) buildFix(c *Case, sources []source, mocks []keyedMock) {
	fix := b.fix(c, sources, mocks)
	c.Fix = renderYAML(fix.caseNode())
	c.FileFix = renderYAML(fix.fileNode())
}

// fix returns the fix that handles the unhandled sources of c, which
// scripts its steps with mocks. A source that no field handles adds
// nothing.
func (b *caseBuilder) fix(c *Case, sources []source, mocks []keyedMock) caseFix {
	var fix caseFix
	for _, s := range sources {
		switch s.kind {
		case sourceParam:
			fix.params = append(fix.params, fixValue{name: s.name, value: b.paramValue(s.name)})
		case sourceSecret:
			fix.secrets = append(fix.secrets, s.name)
		case sourceDAGPrecondition:
			fix.precondition = true
		case sourceAction, sourcePrecondition, sourceRepeatCondition, sourceOutputFile,
			sourceContinuous, sourceForeachBody, sourceWait:
			if m := b.fixMockOf(&fix, s.target, c, mocks); m != nil {
				m.handle(s.kind)
			}
		}
	}
	return fix
}

// paramValue returns the value the fix gives a param: its default, else its
// first enum value, else "".
func (b *caseBuilder) paramValue(name string) any {
	i := slices.IndexFunc(b.dag.ParamDefs, func(def ir.ParamDef) bool { return def.Name == name })
	switch {
	case i < 0:
		return ""
	case b.dag.ParamDefs[i].Default != nil:
		return b.dag.ParamDefs[i].Default
	case len(b.dag.ParamDefs[i].Enum) > 0:
		return b.dag.ParamDefs[i].Enum[0]
	default:
		return ""
	}
}

// fixMockOf returns the mock of the fix for the step or handler labeled
// target. It starts from how the case scripts the step, by its mock under
// mocks or the default mock, under the key the mock is written with.
func (b *caseBuilder) fixMockOf(fix *caseFix, target string, c *Case, mocks []keyedMock) *fixMock {
	i := slices.IndexFunc(b.steps.all, func(t *stepTarget) bool { return t.label == target })
	if i < 0 {
		return nil
	}
	t := b.steps.all[i]
	if j := slices.IndexFunc(fix.mocks, func(m *fixMock) bool { return m.target == t }); j >= 0 {
		return fix.mocks[j]
	}
	m := &fixMock{key: t.label, target: t}
	if k := slices.IndexFunc(mocks, func(km keyedMock) bool { return km.target == t }); k >= 0 {
		m.key = mocks[k].key
	}
	m.setScript(c.script(t))
	fix.mocks = append(fix.mocks, m)
	return m
}

// script returns how the case scripts a step or handler.
func (c *Case) script(t *stepTarget) Script {
	if t.handler != "" {
		return c.Handlers[t.handler]
	}
	return c.Steps[t.step.Name]
}

// setScript copies the fields of a script. A single result is written
// inline. A step without an id takes no outputs under mocks, so the ones the
// default mock publishes for it are left out.
func (m *fixMock) setScript(s Script) {
	for _, r := range s.Results {
		result := fixResult{stdout: r.Stdout, stderr: r.Stderr, outputs: r.Outputs, condition: metText(r.Condition)}
		if r.ExitCode != 0 {
			result.exitCode = &r.ExitCode
		}
		if m.target.step.ID == "" {
			result.outputs = nil
		}
		m.results = append(m.results, result)
	}
	if len(m.results) == 1 {
		m.inline, m.results = &m.results[0], nil
	}
	m.precondition = metText(s.Precondition)
	if s.Review == nil {
		return
	}
	m.review = &fixReview{decision: fixApprove}
	if s.Review.Reject {
		m.review.decision = fixReject
	}
	for _, name := range slices.Sorted(maps.Keys(s.Review.Values)) {
		m.review.values = append(m.review.values, fixValue{name: name, value: s.Review.Values[name]})
	}
}

// metText writes a met or not_met outcome, or "" for none.
func metText(met *bool) string {
	switch {
	case met == nil:
		return ""
	case *met:
		return fixMet
	default:
		return fixNotMet
	}
}

// handle adds the entry that handles one source of the step.
func (m *fixMock) handle(kind sourceKind) {
	step := *m.target.step
	switch kind {
	case sourceAction, sourceForeachBody:
		m.addOutputs()
	case sourceOutputFile, sourceContinuous:
		if m.inline == nil && len(m.results) == 0 {
			m.inline = &fixResult{exitCode: new(0)}
		}
		m.addOutputs()
	case sourcePrecondition:
		m.precondition = fixMet
	case sourceRepeatCondition:
		stop := fixNotMet
		if step.RepeatPolicy.RepeatMode == ir.RepeatModeUntil {
			stop = fixMet
		}
		if m.inline != nil {
			m.results, m.inline = []fixResult{*m.inline}, nil
		}
		if len(m.results) == 0 {
			m.results = []fixResult{{}}
		}
		m.eachResult(func(r *fixResult) {
			if r.condition == "" {
				r.condition = stop
			}
		})
		// The results replace the action, so a successful one publishes the
		// known outputs.
		m.addOutputs()
	case sourceWait:
		m.review = &fixReview{decision: fixApprove, values: waitValues(step)}
	case sourceParam, sourceSecret, sourceDAGPrecondition:
		// Not on a step; buildFix handles them.
	}
}

// eachResult calls fn on each result of the mock, inline or listed.
func (m *fixMock) eachResult(fn func(*fixResult)) {
	if m.inline != nil {
		fn(m.inline)
	}
	for i := range m.results {
		fn(&m.results[i])
	}
}

// addOutputs makes each successful result publish every known output of a
// step with an id, as "" or as null for a type: json output. A mock with no
// result gains one.
func (m *fixMock) addOutputs() {
	step := *m.target.step
	known, runtime := stepOutputs(step)
	if runtime || len(known) == 0 || step.ID == "" {
		return
	}
	if m.inline == nil && len(m.results) == 0 {
		m.inline = &fixResult{}
	}
	m.eachResult(func(r *fixResult) {
		if r.exitCode != nil && *r.exitCode != 0 || r.outputs != nil {
			return
		}
		r.outputs = make(map[string]any, len(known))
		for name, decl := range known {
			r.outputs[name] = ""
			if decl.Type == ir.StepDeclaredOutputTypeJSON {
				r.outputs[name] = nil
			}
		}
	})
}

// waitValues returns the review values of a wait that needs values: TODO for
// each required approval input, and for each required human.task property
// without a default, its first enum or oneOf value, else the empty value of
// its type. A value that the form still rejects is marked for replacement.
func waitValues(step ir.Step) []fixValue {
	if step.HumanTask == nil {
		var values []fixValue
		if step.Approval != nil {
			for _, name := range step.Approval.Required {
				values = append(values, fixValue{name: name, value: fixApprovalInput})
			}
		}
		return values
	}
	var form struct {
		Properties map[string]map[string]any `json:"properties"`
		Required   []string                  `json:"required"`
	}
	if json.Unmarshal(step.HumanTask.Form, &form) != nil {
		return nil
	}
	var values []fixValue
	for _, name := range form.Required {
		property := form.Properties[name]
		if _, ok := property["default"]; ok {
			continue
		}
		value := propertyValue(property)
		_, err := spec.ValidateHumanTaskInputs(propertyForm(name, property), map[string]any{name: value}, false)
		values = append(values, fixValue{name: name, value: value, replace: err != nil})
	}
	return values
}

// propertyValue returns the first enum or oneOf value of a form property,
// else the empty value of its type.
func propertyValue(property map[string]any) any {
	if enum, ok := property["enum"].([]any); ok && len(enum) > 0 {
		return enum[0]
	}
	if oneOf, ok := property["oneOf"].([]any); ok && len(oneOf) > 0 {
		if option, ok := oneOf[0].(map[string]any); ok {
			if value, ok := option["const"]; ok {
				return value
			}
		}
	}
	switch property["type"] {
	case "integer", "number":
		return 0
	case "boolean":
		return false
	default:
		return ""
	}
}

// propertyForm returns a form that holds only the named property, so a
// value is checked against that property's constraints alone.
func propertyForm(name string, property map[string]any) json.RawMessage {
	form, _ := json.Marshal(map[string]any{
		"type":       "object",
		"properties": map[string]any{name: property},
	})
	return form
}

// caseNode returns the case fields of the fix: params, precondition, and
// mocks.
func (f caseFix) caseNode() *yaml.Node {
	root := mappingNode()
	f.addParams(root)
	if f.precondition {
		addEntry(root, "precondition", valueNode(fixMet))
	}
	f.addMocks(root)
	return root
}

// addParams adds the params of the fix to n, if any.
func (f caseFix) addParams(n *yaml.Node) {
	if len(f.params) == 0 {
		return
	}
	params := mappingNode()
	for _, p := range f.params {
		addEntry(params, p.name, valueNode(p.value))
	}
	addEntry(n, "params", params)
}

// addMocks adds the mocks of the fix to n, if any.
func (f caseFix) addMocks(n *yaml.Node) {
	if len(f.mocks) == 0 {
		return
	}
	mocks := mappingNode()
	for _, m := range f.mocks {
		addEntry(mocks, m.key, m.node())
	}
	addEntry(n, "mocks", mocks)
}

// fileNode returns the test file fields of the fix: secrets.
func (f caseFix) fileNode() *yaml.Node {
	root := mappingNode()
	if len(f.secrets) > 0 {
		secrets := mappingNode()
		for _, name := range f.secrets {
			addEntry(secrets, name, valueNode(""))
		}
		addEntry(root, "secrets", secrets)
	}
	return root
}

// node writes the mock with its result fields first, then its step fields.
func (m *fixMock) node() *yaml.Node {
	n := mappingNode()
	if m.inline != nil {
		m.inline.addFields(n)
	}
	if m.results != nil {
		results := &yaml.Node{Kind: yaml.SequenceNode}
		for _, r := range m.results {
			item := mappingNode()
			r.addFields(item)
			results.Content = append(results.Content, flowIfEmpty(item))
		}
		addEntry(n, "results", results)
	}
	if m.precondition != "" {
		addEntry(n, "precondition", valueNode(m.precondition))
	}
	if m.review != nil {
		review := mappingNode()
		addEntry(review, "decision", valueNode(m.review.decision))
		if len(m.review.values) > 0 {
			values := mappingNode()
			for _, v := range m.review.values {
				value := valueNode(v.value)
				if v.replace {
					value.LineComment = fixReplaceComment
				}
				addEntry(values, v.name, value)
			}
			addEntry(review, "values", values)
		}
		addEntry(n, "review", review)
	}
	return flowIfEmpty(n)
}

// addFields writes the result's fields. An inline result with no other
// field writes exit_code, so it stays a result.
func (r fixResult) addFields(n *yaml.Node) {
	start := len(n.Content)
	if r.stdout != "" {
		addEntry(n, "stdout", valueNode(r.stdout))
	}
	if r.stderr != "" {
		addEntry(n, "stderr", valueNode(r.stderr))
	}
	if r.outputs != nil {
		addEntry(n, "outputs", flowIfEmpty(valueNode(r.outputs)))
	}
	if r.condition != "" {
		addEntry(n, "condition", valueNode(r.condition))
	}
	if r.exitCode != nil || len(n.Content) == start {
		code := 0
		if r.exitCode != nil {
			code = *r.exitCode
		}
		n.Content = append(n.Content[:start], append([]*yaml.Node{valueNode("exit_code"), valueNode(code)}, n.Content[start:]...)...)
	}
}

func mappingNode() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode}
}

func addEntry(n *yaml.Node, key string, value *yaml.Node) {
	n.Content = append(n.Content, valueNode(key), value)
}

// valueNode encodes a value decoded from YAML or JSON, which always
// encodes.
func valueNode(value any) *yaml.Node {
	var n yaml.Node
	_ = n.Encode(value)
	return &n
}

// flowIfEmpty writes an empty map as {}.
func flowIfEmpty(n *yaml.Node) *yaml.Node {
	if n.Kind == yaml.MappingNode && len(n.Content) == 0 {
		n.Style = yaml.FlowStyle
	}
	return n
}

// renderYAML writes the fields of n as YAML indented by two spaces, or ""
// when n has none.
func renderYAML(n *yaml.Node) string {
	if len(n.Content) == 0 {
		return ""
	}
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if encoder.Encode(n) != nil || encoder.Close() != nil {
		return ""
	}
	return buf.String()
}
