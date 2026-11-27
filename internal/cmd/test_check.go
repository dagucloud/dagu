// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/jsonutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
	"github.com/dagucloud/dagu/v2/internal/dagtest"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/agent"
	"github.com/dagucloud/dagu/v2/internal/runtime/stepmock"
)

// notStartedToken is the status of a step or handler that never started.
const notStartedToken = "not_started"

// Streams a step assertion reads.
const (
	streamStdout = "stdout"
	streamStderr = "stderr"
)

// check returns every assertion of expect that the case's run fails. status
// is the status of the run's last attempt. A run stopped by a step that
// repeats without a limit, and a run that ended without a terminal status,
// fail before any assertion.
func (c *testCase) check(expect dagtest.Expect, status *ir.DAGRunStatus) []caseFailure {
	if failure := unfinishedRun(status); failure != nil {
		return []caseFailure{*failure}
	}

	var failures []caseFailure
	if expect.Status != "" && expect.Status != status.Status.String() {
		failures = append(failures, caseFailure{field: failureStatus, expected: expect.Status, actual: status.Status.String()})
	}
	if expect.Attempts != nil && *expect.Attempts != c.attempts {
		failures = append(failures, caseFailure{field: failureAttempts, expected: *expect.Attempts, actual: c.attempts})
	}
	for _, s := range expect.Steps {
		failures = append(failures, c.checkStep(s, status)...)
	}
	if expect.Notifications != nil && !pairNotifications(expect.Notifications, c.notifications) {
		failures = append(failures, caseFailure{
			field:    failureNotifications,
			expected: expectedNotifications(c.dag, expect.Notifications),
			actual:   sentNotifications(c.dag, c.notifications),
		})
	}
	return failures
}

// unfinishedRun returns the failure of a run that did not finish: one
// stopped by a step that repeats without a limit, or one that ended without
// a terminal status. status is the status of the run's last attempt. It
// returns nil for a run that finished.
func unfinishedRun(status *ir.DAGRunStatus) *caseFailure {
	if node := unboundedStep(status); node != nil {
		return &caseFailure{
			step:    stepLabel(node.Step),
			field:   failureLimit,
			actual:  fmt.Sprint(node.Step.RepeatPolicy.Limit),
			message: node.Error,
		}
	}
	if !status.Status.IsTerminal() {
		return &caseFailure{field: failureError, actual: fmt.Sprintf("the run ended %s, which is not a terminal status", status.Status)}
	}
	return nil
}

// expectFromRun returns the exact facts of the case's run as assertions:
// the run's status and attempts, the status and attempts of every step in
// the order the DAG declares them and then of every lifecycle handler, and
// the notifications the run would have sent. status is the status of the
// run's last attempt. When it is terminal, check passes the facts.
func (c *testCase) expectFromRun(status *ir.DAGRunStatus) dagtest.Expect {
	expect := dagtest.Expect{
		Status:        status.Status.String(),
		Attempts:      new(c.attempts),
		Notifications: make([]dagtest.Notification, 0, len(c.notifications)),
	}
	for _, step := range c.dag.Steps {
		expect.Steps = append(expect.Steps, c.stepFacts(dagtest.StepExpect{Key: stepLabel(step), Step: step.Name}, status))
	}
	for _, h := range lifecycleHandlers(c.dag.HandlerOn) {
		expect.Steps = append(expect.Steps, c.stepFacts(h, status))
	}
	for _, n := range c.notifications {
		expect.Notifications = append(expect.Notifications, dagtest.Notification{Kind: string(n.Kind), Step: n.Step, To: n.To})
	}
	return expect
}

// stepFacts returns s with the status and attempts of its step or handler
// in the case's run.
func (c *testCase) stepFacts(s dagtest.StepExpect, status *ir.DAGRunStatus) dagtest.StepExpect {
	s.Status = nodeStatusToken(expectedNode(status, c.waited, s))
	s.Attempts = new(c.stepAttempts(s))
	return s
}

// handlerKeyPrefix starts the step key of a lifecycle handler.
const handlerKeyPrefix = "handler_on."

// lifecycleHandlers returns the lifecycle handlers that on sets, each with
// its step key, in the order init, success, failure, abort, exit, wait.
func lifecycleHandlers(on ir.HandlerOn) []dagtest.StepExpect {
	var handlers []dagtest.StepExpect
	for _, h := range []struct {
		event string
		kind  ir.HandlerType
		step  *ir.Step
	}{
		{"init", ir.HandlerOnInit, on.Init},
		{"success", ir.HandlerOnSuccess, on.Success},
		{"failure", ir.HandlerOnFailure, on.Failure},
		{"abort", ir.HandlerOnAbort, on.Abort},
		{"exit", ir.HandlerOnExit, on.Exit},
		{"wait", ir.HandlerOnWait, on.Wait},
	} {
		if h.step != nil {
			handlers = append(handlers, dagtest.StepExpect{Key: handlerKeyPrefix + h.event, Handler: h.kind})
		}
	}
	return handlers
}

// checkStep returns the failed assertions on one step or handler: status,
// attempts, outputs in the order listed, stdout, then stderr.
func (c *testCase) checkStep(s dagtest.StepExpect, status *ir.DAGRunStatus) []caseFailure {
	var failures []caseFailure
	node := expectedNode(status, c.waited, s)
	if actual := nodeStatusToken(node); s.Status != "" && s.Status != actual {
		failures = append(failures, caseFailure{step: s.Key, field: failureStatus, expected: s.Status, actual: actual})
	}
	if actual := c.stepAttempts(s); s.Attempts != nil && *s.Attempts != actual {
		failures = append(failures, caseFailure{step: s.Key, field: failureAttempts, expected: *s.Attempts, actual: actual})
	}
	published := publishedOutputs(node)
	masker := masking.NewValueMasker(c.host.values)
	mask := func(v any) json.RawMessage { return c.host.mask(masker, v) }
	for _, o := range s.Outputs {
		if failure, ok := checkOutput(s.Key, o, published, mask); !ok {
			failures = append(failures, failure)
		}
	}
	// A step with no execution has no stream.
	var stdout, stderr string
	if nodeAttempts(node, s.Handler != "") > 0 {
		stdout, stderr = node.Stdout, node.Stderr
	}
	failures = append(failures, c.checkStream(s.Key, streamStdout, s.Stdout, stdout)...)
	failures = append(failures, c.checkStream(s.Key, streamStderr, s.Stderr, stderr)...)
	return append(failures, c.checkCall(s, node)...)
}

// lastCall returns what the latest execution of the step or handler that s
// names was called with, or false when its node shows no execution. node is
// its node in the run's last attempt.
func (c *testCase) lastCall(s dagtest.StepExpect, node *ir.Node) (stepmock.Call, bool) {
	if c.scripts == nil || nodeAttempts(node, s.Handler != "") == 0 {
		return stepmock.Call{}, false
	}
	if s.Handler != "" {
		return c.scripts.LastHandlerCall(s.Handler)
	}
	return c.scripts.LastCall(s.Step)
}

// checkCall returns the failed parts of a called_with assertion: run, its
// text, contains, then matches; with inputs in the order listed; then env
// in the order listed. A step with no execution fails it once. Both values
// of a failure are compact JSON with the call's secrets and the host values
// masked, and a failure has no actual value for an input or variable the
// call lacks.
func (c *testCase) checkCall(s dagtest.StepExpect, node *ir.Node) []caseFailure {
	expect := s.CalledWith
	if expect == nil {
		return nil
	}
	call, ok := c.lastCall(s, node)
	if !ok {
		return []caseFailure{{step: s.Key, field: failureCall}}
	}
	masker := masking.NewValueMasker(append(slices.Clone(call.Secrets), c.host.values...))
	var failures []caseFailure
	// fail adds a failed part. A part the call lacks has no actual value.
	fail := func(field string, expected, actual any, present bool) {
		f := caseFailure{step: s.Key, field: field, expected: c.host.mask(masker, expected)}
		if present {
			f.actual = c.host.mask(masker, actual)
		}
		failures = append(failures, f)
	}
	if r := expect.Run; r != nil {
		if r.Text != nil && *r.Text != call.Run {
			fail(failureCallRun, *r.Text, call.Run, true)
		}
		if r.Contains != nil && !strings.Contains(call.Run, *r.Contains) {
			fail(failureCallRun+failureContains, *r.Contains, call.Run, true)
		}
		if r.Matches != nil && !r.Matches.MatchString(call.Run) {
			fail(failureCallRun+failureMatches, r.Matches.String(), call.Run, true)
		}
	}
	for _, in := range expect.With {
		value, ok := call.With[in.Name]
		if !ok || !outputMatches(dagtest.OutputExpect{Value: in.Value}, outputText(value)) {
			fail(failureCallInputPrefix+in.Name, in.Value, value, ok)
		}
	}
	for _, e := range expect.Env {
		value, ok := call.Env[e.Name]
		if !ok || value != e.Value {
			fail(failureCallEnvPrefix+e.Name, e.Value, value, ok)
		}
	}
	return failures
}

// maskedJSON returns v as compact JSON with masker's values masked. Masked
// text that is no longer JSON, such as a masked number, is a JSON string.
func maskedJSON(masker *masking.Masker, v any) json.RawMessage {
	data, _ := jsonutil.MarshalUnescaped(v)
	masked := masker.MaskString(string(data))
	if !json.Valid([]byte(masked)) {
		masked = quoteJSON(masked)
	}
	return json.RawMessage(masked)
}

// stepAttempts counts the executions of a step or handler in every attempt
// of the run. Each run attempt, a resume included, logs to a directory of
// its own and starts each step at most once there, so a node's stdout log
// names one node start. A retry or resume carries over the nodes it does
// not run again with their log paths, so each node counts once by its log.
func (c *testCase) stepAttempts(s dagtest.StepExpect) int {
	counts := make(map[string]int)
	for _, status := range c.ended {
		node := handlerNode(status, s.Handler)
		if s.Handler == "" {
			node = stepNode(status, s.Step)
		}
		if node != nil && node.Stdout != "" {
			counts[node.Stdout] = nodeAttempts(node, s.Handler != "")
		}
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	return total
}

// unboundedStep returns the step that stopped the run because it repeats
// without a limit, read from the step's own error, or nil. The run's error
// may name a later handler failure instead.
func unboundedStep(status *ir.DAGRunStatus) *ir.Node {
	for _, node := range status.Nodes {
		unbounded := &runtime.UnboundedRepeatError{Step: node.Step.Name, Limit: node.Step.RepeatPolicy.Limit}
		if node.Status == ir.NodeFailed && node.Error == unbounded.Error() {
			return node
		}
	}
	return nil
}

// expectedNode returns the node of the step or handler that an assertion
// names, or nil when it never started. A handler that ran before a resume
// shows only in the latest of the waited statuses in which it started.
func expectedNode(status *ir.DAGRunStatus, waited []*ir.DAGRunStatus, s dagtest.StepExpect) *ir.Node {
	if s.Handler == "" {
		return stepNode(status, s.Step)
	}
	node := handlerNode(status, s.Handler)
	for i := len(waited) - 1; i >= 0 && nodeStatusToken(node) == notStartedToken; i-- {
		node = handlerNode(waited[i], s.Handler)
	}
	return node
}

// stepNode returns the node of the named step in status, or nil.
func stepNode(status *ir.DAGRunStatus, name string) *ir.Node {
	if i := slices.IndexFunc(status.Nodes, func(n *ir.Node) bool { return n.Step.Name == name }); i >= 0 {
		return status.Nodes[i]
	}
	return nil
}

// handlerNode returns the node of a lifecycle handler in status.
func handlerNode(status *ir.DAGRunStatus, kind ir.HandlerType) *ir.Node {
	switch kind {
	case ir.HandlerOnInit:
		return status.OnInit
	case ir.HandlerOnSuccess:
		return status.OnSuccess
	case ir.HandlerOnFailure:
		return status.OnFailure
	case ir.HandlerOnAbort:
		return status.OnAbort
	case ir.HandlerOnExit:
		return status.OnExit
	case ir.HandlerOnWait:
		return status.OnWait
	}
	return nil
}

func nodeStatusToken(node *ir.Node) string {
	if node == nil {
		return notStartedToken
	}
	return node.Status.String()
}

// nodeAttempts returns how many times the action of a step or handler ran
// in one attempt, counting retries and repeat iterations. A human.task has
// no action. A handler runs its action at most once and records no count,
// so one that started counts once.
func nodeAttempts(node *ir.Node, handler bool) int {
	switch {
	case node == nil || node.Step.HumanTask != nil:
		return 0
	case handler && node.Status.IsDone() && node.Status != ir.NodeSkipped:
		return 1
	case handler:
		return 0
	default:
		return node.RetryCount + node.DoneCount
	}
}

// publishedOutputs returns the named outputs a step published, as
// ${steps.<id>.outputs.<name>} reads them, or nil.
func publishedOutputs(node *ir.Node) map[string]any {
	if node == nil {
		return nil
	}
	for _, raw := range []*string{node.StepOutputsValue, node.OutputsValue} {
		var outputs map[string]any
		if raw != nil && decodeJSON(*raw, &outputs) == nil && outputs != nil {
			return outputs
		}
	}
	return nil
}

// checkOutput returns the failure of an output assertion, or false when
// the step published a matching value. Both values of a failure are
// compact JSON as mask writes them: the published one as a JSON string for
// a text output, and as its JSON text for a type: json output.
// A failure has no actual value when the step published no such output.
func checkOutput(key string, o dagtest.OutputExpect, published map[string]any, mask func(any) json.RawMessage) (caseFailure, bool) {
	failure := caseFailure{step: key, field: failureOutputPrefix + o.Name, expected: mask(o.Value)}
	value, ok := published[o.Name]
	if !ok {
		return failure, false
	}
	text := outputText(value)
	if outputMatches(o, text) {
		return caseFailure{}, true
	}
	var compact bytes.Buffer
	if !o.JSON || json.Compact(&compact, []byte(text)) != nil {
		compact.Reset()
		compact.WriteString(quoteJSON(text))
	}
	failure.actual = mask(json.RawMessage(compact.Bytes()))
	return failure, false
}

// outputText returns the text a published output resolves to: a string as
// written, and any other value as its JSON text.
func outputText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	data, _ := jsonutil.MarshalUnescaped(value)
	return string(data)
}

// outputMatches reports whether the text of a published output matches the
// expected value (Spec 101, Assertions). A type: json output, and any
// expected value but a string, match by the JSON rule against the text read
// as JSON. Text that is not JSON matches no such value.
func outputMatches(o dagtest.OutputExpect, text string) bool {
	if expected, ok := o.Value.(string); ok && !o.JSON {
		return text == expected
	}
	var actual any
	if decodeJSON(text, &actual) != nil {
		return false
	}
	expected, ok := jsonValue(o.Value)
	return ok && jsonMatches(expected, actual)
}

// jsonMatches applies the JSON rule: an object matches when every key it
// lists has a matching value, recursively, and an array or a scalar matches
// only an equal value.
func jsonMatches(expected, actual any) bool {
	object, ok := expected.(map[string]any)
	if !ok {
		return jsonEqual(expected, actual)
	}
	got, ok := actual.(map[string]any)
	if !ok {
		return false
	}
	for key, value := range object {
		if v, ok := got[key]; !ok || !jsonMatches(value, v) {
			return false
		}
	}
	return true
}

// jsonEqual reports whether two decoded JSON values are equal. Numbers
// compare by value.
func jsonEqual(a, b any) bool {
	switch x := a.(type) {
	case json.Number:
		y, ok := b.(json.Number)
		if !ok {
			return false
		}
		rx, okx := new(big.Rat).SetString(x.String())
		ry, oky := new(big.Rat).SetString(y.String())
		return okx && oky && rx.Cmp(ry) == 0
	case []any:
		y, ok := b.([]any)
		return ok && slices.EqualFunc(x, y, jsonEqual)
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for key, value := range x {
			if v, ok := y[key]; !ok || !jsonEqual(value, v) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}

// jsonValue returns a value decoded from YAML as JSON decodes it, with
// numbers as json.Number, or false when it has no JSON form.
func jsonValue(v any) (any, bool) {
	var out any
	data, err := json.Marshal(v)
	if err != nil || decodeJSON(string(data), &out) != nil {
		return nil, false
	}
	return out, true
}

// decodeJSON decodes exactly one JSON value from text, keeping numbers as
// json.Number.
func decodeJSON(text string, target any) error {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("more than one JSON value")
	}
	return nil
}

// checkStream returns the failed parts of a stream assertion on the stream
// of the step's last execution: contains, then matches. path is the step's
// log of the stream, or empty for a step with no execution, which fails
// every part.
func (c *testCase) checkStream(key, stream string, expect *dagtest.StreamExpect, path string) []caseFailure {
	if expect == nil {
		return nil
	}
	var text string
	if path != "" {
		var err error
		if text, err = c.lastExecution(path); err != nil {
			return []caseFailure{{field: failureError, actual: fmt.Sprintf("step %s %s: %v", key, stream, err)}}
		}
	}
	ran := path != ""
	var failures []caseFailure
	if expect.Contains != nil && (!ran || !strings.Contains(text, *expect.Contains)) {
		failures = append(failures, caseFailure{step: key, field: stream + failureContains, expected: *expect.Contains})
	}
	if expect.Matches != nil && (!ran || !expect.Matches.MatchString(text)) {
		failures = append(failures, caseFailure{step: key, field: stream + failureMatches, expected: expect.Matches.String()})
	}
	return failures
}

// pairNotifications reports whether the run sent as many notifications as
// expected lists, and each expected one pairs with a different sent one,
// in any order.
func pairNotifications(expected []dagtest.Notification, sent []agent.Notification) bool {
	if len(expected) != len(sent) {
		return false
	}
	// owner holds, for each sent notification, the index of the expected
	// one paired with it, or -1. Pairs move along augmenting paths, since an
	// item without step or to may take a notification a stricter item needs.
	owner := make([]int, len(sent))
	for i := range owner {
		owner[i] = -1
	}
	var pair func(i int, seen []bool) bool
	pair = func(i int, seen []bool) bool {
		for j := range sent {
			if seen[j] || !notificationMatches(expected[i], sent[j]) {
				continue
			}
			seen[j] = true
			if owner[j] < 0 || pair(owner[j], seen) {
				owner[j] = i
				return true
			}
		}
		return false
	}
	for i := range expected {
		if !pair(i, make([]bool, len(sent))) {
			return false
		}
	}
	return true
}

// notificationMatches reports whether a sent notification has the expected
// kind, and the step and the set of recipients when the item sets them.
func notificationMatches(expected dagtest.Notification, sent agent.Notification) bool {
	if expected.Kind != string(sent.Kind) || expected.Step != "" && expected.Step != sent.Step {
		return false
	}
	if expected.To == nil {
		return true
	}
	return sameSet(expected.To, sent.To)
}

func sameSet(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(slices.Compact(x), slices.Compact(y))
}

// stepLabel names a step in the report: its id, or its name when it has
// none.
func stepLabel(step ir.Step) string {
	if step.ID != "" {
		return step.ID
	}
	return step.Name
}

// stepLabelOf returns the label of the named step of dag, or the name of a
// step dag does not declare.
func stepLabelOf(dag *ir.DAG, name string) string {
	if i := slices.IndexFunc(dag.Steps, func(s ir.Step) bool { return s.Name == name }); i >= 0 {
		return stepLabel(dag.Steps[i])
	}
	return name
}

// stepLabels returns the labels of the named steps in the order dag
// declares them.
func stepLabels(dag *ir.DAG, names []string) []string {
	var labels []string
	for _, step := range dag.Steps {
		if slices.Contains(names, step.Name) {
			labels = append(labels, stepLabel(step))
		}
	}
	return labels
}
