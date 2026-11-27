// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"encoding/json"
	"slices"
	"strconv"

	"github.com/dagucloud/dagu/v2/internal/dagtest"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// What --update did with a case's expect, as the JSON report's update field
// names it (Spec 101, Authoring Flags).
const (
	updateUpdated   = "updated"
	updateUnchanged = "unchanged"
	updateKept      = "kept"
)

// Why --update kept a case's expect as written.
const (
	keptInvalid    = "the case is invalid"
	keptUnfinished = "the run did not finish"
)

// caseUpdate is what --update did with a case's expect.
type caseUpdate struct {
	// outcome is updateUpdated, updateUnchanged, or updateKept.
	outcome string
	// reason says why a kept expect stays as written.
	reason string
	// kept lists the assertions of an updated or unchanged expect that stay
	// as written because the run's value holds a host value.
	kept []keptValue
}

// line writes the outcome as the last line of a case in the text report.
func (u *caseUpdate) line() string {
	if u.outcome == updateKept {
		return "expect kept: " + u.reason
	}
	return "expect " + u.outcome
}

// updateExpect returns the expect hook of --update for the named case of
// the test file that update rewrites. A run that finished is checked
// against its facts merged into the written expect, which update then
// holds, and any other run against the written expect. out records what
// happened to the expect.
func updateExpect(update *dagtest.Update, name string, out *caseUpdate) caseExpect {
	return func(written dagtest.Expect, tc *testCase, status *ir.DAGRunStatus) dagtest.Expect {
		if unfinishedRun(status) != nil {
			return written
		}
		merged, kept := tc.updatedExpect(written, status)
		changed, err := update.Set(name, merged)
		switch {
		case err != nil:
			out.reason = err.Error()
			return written
		case changed:
			out.outcome = updateUpdated
		default:
			out.outcome = updateUnchanged
		}
		out.kept = kept
		return merged
	}
}

// updatedExpect returns written with the exact facts of the case's run
// merged in, as --update rewrites it (Spec 101, Authoring Flags), and the
// assertions that stay as written because the run's value holds a host
// value. An asserted status, attempts, or output takes the run's value,
// and asserted notifications take the sent ones, unless the assertion
// already holds. Each step and handler without an assertion follows the
// written ones, in the order of expectFromRun, with its status, and its
// attempts when more than one. Stream assertions stay as written. status
// is the status of the run's last attempt, which finished.
func (c *testCase) updatedExpect(written dagtest.Expect, status *ir.DAGRunStatus) (dagtest.Expect, []keptValue) {
	facts := c.expectFromRun(status)
	merged := written
	if written.Status != "" {
		merged.Status = facts.Status
	}
	if written.Attempts != nil && *written.Attempts != *facts.Attempts {
		merged.Attempts = facts.Attempts
	}
	merged.Steps = make([]dagtest.StepExpect, 0, len(facts.Steps))
	var kept []keptValue
	for _, s := range written.Steps {
		step, stepKept := c.updatedStep(s, status)
		merged.Steps = append(merged.Steps, step)
		kept = append(kept, stepKept...)
	}
	for _, f := range facts.Steps {
		if slices.ContainsFunc(written.Steps, func(s dagtest.StepExpect) bool { return s.Step == f.Step && s.Handler == f.Handler }) {
			continue
		}
		if *f.Attempts <= 1 {
			f.Attempts = nil
		}
		merged.Steps = append(merged.Steps, f)
	}
	if written.Notifications != nil && !pairNotifications(written.Notifications, c.notifications) {
		merged.Notifications = facts.Notifications
	}
	return merged, kept
}

// updatedStep returns the assertions on one step or handler with the facts
// of the case's run merged in, and those that stay as written because the
// run's value holds a host value.
func (c *testCase) updatedStep(s dagtest.StepExpect, status *ir.DAGRunStatus) (dagtest.StepExpect, []keptValue) {
	facts := c.stepFacts(s, status)
	if s.Status != "" {
		s.Status = facts.Status
	}
	if s.Attempts != nil && *s.Attempts != *facts.Attempts {
		s.Attempts = facts.Attempts
	}
	node := expectedNode(status, c.waited, s)
	call, called := c.lastCall(s, node)
	guard := &hostGuard{host: &c.host, step: s.Key, call: call}
	published := publishedOutputs(node)
	s.Outputs = slices.Clone(s.Outputs)
	for i, o := range s.Outputs {
		if value, ok := published[o.Name]; ok {
			s.Outputs[i].Value = guard.value(failureOutputPrefix+o.Name, o.Value, updatedOutput(o, outputText(value)))
		}
	}
	if called && s.CalledWith != nil {
		s.CalledWith = updatedCall(*s.CalledWith, guard)
	}
	return s, guard.kept
}

// updatedCall returns a called_with assertion with the values of the call
// that guard holds merged in. A run text takes the call's text, and a with
// input or a variable that the call has takes its value, the listed keys
// only of an object, as an output does. A run given as contains and
// matches stays as written, and so does an input or a variable the call
// lacks, and a value that guard keeps out.
func updatedCall(expect dagtest.CallExpect, guard *hostGuard) *dagtest.CallExpect {
	call := guard.call
	if r := expect.Run; r != nil && r.Text != nil {
		expect.Run = &dagtest.RunExpect{Text: new(guard.value(failureCallRun, *r.Text, call.Run).(string))}
	}
	expect.With = slices.Clone(expect.With)
	for i, in := range expect.With {
		if value, ok := call.With[in.Name]; ok {
			updated := updatedOutput(dagtest.OutputExpect{Value: in.Value}, outputText(value))
			expect.With[i].Value = guard.value(failureCallInputPrefix+in.Name, in.Value, updated)
		}
	}
	expect.Env = slices.Clone(expect.Env)
	for i, e := range expect.Env {
		if value, ok := call.Env[e.Name]; ok {
			expect.Env[i].Value = guard.variable(e.Name, e.Value, value)
		}
	}
	return &expect
}

// updatedOutput returns the value an output assertion takes from the text
// the step published: the expected value when the text matches it, the text
// when a string must match it exactly, and otherwise the text read as JSON,
// merged into the expected value by mergeJSON. An expected object, and the
// value of a type: json output, stay when the text is not JSON.
func updatedOutput(o dagtest.OutputExpect, text string) any {
	if outputMatches(o, text) {
		return o.Value
	}
	if _, ok := o.Value.(string); ok && !o.JSON {
		return text
	}
	var actual any
	if decodeJSON(text, &actual) == nil {
		return mergeJSON(o.Value, actual)
	}
	if _, object := o.Value.(map[string]any); object || o.JSON {
		return o.Value
	}
	// Text that is not JSON matches only a string.
	return text
}

// mergeJSON returns actual in place of expected, except that an expected
// object keeps its own keys, each with the actual value under it merged in,
// and keeps its value where actual is not an object or lacks a key. A value
// that already matches stays as written, and so does one in place of an
// actual value that YAML cannot write exactly.
func mergeJSON(expected, actual any) any {
	if want, ok := jsonValue(expected); ok && jsonMatches(want, actual) {
		return expected
	}
	object, ok := expected.(map[string]any)
	if !ok {
		if value, exact := yamlValue(actual); exact {
			return value
		}
		return expected
	}
	got, ok := actual.(map[string]any)
	if !ok {
		return expected
	}
	merged := make(map[string]any, len(object))
	for key, value := range object {
		merged[key] = value
		if v, ok := got[key]; ok {
			merged[key] = mergeJSON(value, v)
		}
	}
	return merged
}

// yamlValue returns a value decoded from JSON with each number as the
// int64, uint64, or float64 that YAML writes it as, or false when a number
// would read back as another, such as an integer beyond uint64 or a decimal
// with more digits than a float64 holds.
func yamlValue(v any) (any, bool) {
	switch x := v.(type) {
	case json.Number:
		return yamlNumber(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for key, value := range x {
			converted, ok := yamlValue(value)
			if !ok {
				return nil, false
			}
			out[key] = converted
		}
		return out, true
	case []any:
		out := make([]any, len(x))
		for i, value := range x {
			converted, ok := yamlValue(value)
			if !ok {
				return nil, false
			}
			out[i] = converted
		}
		return out, true
	}
	return v, true
}

// yamlNumber returns n as the int64, uint64, or float64 that YAML writes it
// as, and whether that reads back as a number of equal value.
func yamlNumber(n json.Number) (any, bool) {
	if i, err := n.Int64(); err == nil {
		return i, true
	}
	if u, err := strconv.ParseUint(n.String(), 10, 64); err == nil {
		return u, true
	}
	f, err := n.Float64()
	if err != nil {
		return nil, false
	}
	// yaml.v3 writes a float64 as its shortest decimal form.
	return f, jsonEqual(n, json.Number(strconv.FormatFloat(f, 'g', -1, 64)))
}
