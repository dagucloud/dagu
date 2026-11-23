// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"slices"

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
		merged := tc.updatedExpect(written, status)
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
		return merged
	}
}

// updatedExpect returns written with the exact facts of the case's run
// merged in, as --update rewrites it (Spec 101, Authoring Flags). An
// asserted status, attempts, or output takes the run's value, and asserted
// notifications take the sent ones, unless the assertion already holds.
// Each step and handler without an assertion follows the written ones, in
// the order of expectFromRun, with its status, and its attempts when more
// than one. Stream assertions stay as written. status is the status of the
// run's last attempt, which finished.
func (c *testCase) updatedExpect(written dagtest.Expect, status *ir.DAGRunStatus) dagtest.Expect {
	facts := c.expectFromRun(status)
	merged := written
	if written.Status != "" {
		merged.Status = facts.Status
	}
	if written.Attempts != nil && *written.Attempts != *facts.Attempts {
		merged.Attempts = facts.Attempts
	}
	merged.Steps = make([]dagtest.StepExpect, 0, len(facts.Steps))
	for _, s := range written.Steps {
		merged.Steps = append(merged.Steps, c.updatedStep(s, status))
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
	return merged
}

// updatedStep returns the assertions on one step or handler with the facts
// of the case's run merged in.
func (c *testCase) updatedStep(s dagtest.StepExpect, status *ir.DAGRunStatus) dagtest.StepExpect {
	facts := c.stepFacts(s, status)
	if s.Status != "" {
		s.Status = facts.Status
	}
	if s.Attempts != nil && *s.Attempts != *facts.Attempts {
		s.Attempts = facts.Attempts
	}
	published := publishedOutputs(expectedNode(status, c.waited, s))
	s.Outputs = slices.Clone(s.Outputs)
	for i, o := range s.Outputs {
		if value, ok := published[o.Name]; ok {
			s.Outputs[i].Value = updatedOutput(o, outputText(value))
		}
	}
	return s
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
// that already matches stays as written.
func mergeJSON(expected, actual any) any {
	if want, ok := jsonValue(expected); ok && jsonMatches(want, actual) {
		return expected
	}
	object, ok := expected.(map[string]any)
	if !ok {
		return actual
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
