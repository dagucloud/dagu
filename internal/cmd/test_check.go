// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"slices"

	"github.com/dagucloud/dagu/v2/internal/dagtest"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

// notStartedToken is the status of a step or handler that never started.
const notStartedToken = "not_started"

// unsupportedAssertions reports the assertions that this version of dagu
// test does not check yet, so a case never passes without checking one.
func unsupportedAssertions(expect dagtest.Expect) []dagtest.Finding {
	var errs []dagtest.Finding
	unsupported := func(label string) {
		errs = append(errs, dagtest.Finding{Source: label, Message: "is not supported by this version of dagu test"})
	}
	if expect.Attempts != nil {
		unsupported("expect.attempts")
	}
	for _, s := range expect.Steps {
		label := "expect.steps." + s.Key
		if len(s.Outputs) > 0 {
			unsupported(label + ".outputs")
		}
		if s.Stdout != nil {
			unsupported(label + ".stdout")
		}
		if s.Stderr != nil {
			unsupported(label + ".stderr")
		}
	}
	if expect.Notifications != nil {
		unsupported("expect.notifications")
	}
	return errs
}

// realChildRuns reports each real step and handler of c that starts a child
// run in the case, as dag.run and parallel do. This version of dagu test
// does not check a child DAG against the fail-closed rule, so such a case
// is invalid instead of running the child's actions.
func realChildRuns(dag *ir.DAG, c dagtest.Case) []dagtest.Finding {
	var errs []dagtest.Finding
	check := func(label string, step *ir.Step, real bool) {
		if real && step != nil && step.SubDAG != nil && step.ExecutorConfig.Type != ir.ExecutorTypeDAGEnqueue {
			errs = append(errs, dagtest.Finding{
				Source:  label,
				Message: "a real step that starts a child run is not supported by this version of dagu test; mock it",
			})
		}
	}
	for i := range dag.Steps {
		check("step "+stepLabel(dag.Steps[i]), &dag.Steps[i], c.Steps[dag.Steps[i].Name].Real)
	}
	for _, h := range []struct {
		event string
		kind  ir.HandlerType
		step  *ir.Step
	}{
		{"init", ir.HandlerOnInit, dag.HandlerOn.Init},
		{"success", ir.HandlerOnSuccess, dag.HandlerOn.Success},
		{"failure", ir.HandlerOnFailure, dag.HandlerOn.Failure},
		{"abort", ir.HandlerOnAbort, dag.HandlerOn.Abort},
		{"exit", ir.HandlerOnExit, dag.HandlerOn.Exit},
		{"wait", ir.HandlerOnWait, dag.HandlerOn.Wait},
	} {
		check("handler_on."+h.event, h.step, c.Handlers[h.kind].Real)
	}
	return errs
}

// checkCase returns every assertion of expect that the run's final status
// fails. waited holds the statuses at which the run's last attempt waited
// before it resumed, where a handler that ran before a resume shows. A run
// stopped by a step that repeats without a limit, and a run that ended
// without a terminal status, fail before any assertion.
func checkCase(expect dagtest.Expect, status *ir.DAGRunStatus, waited []*ir.DAGRunStatus) []caseFailure {
	if node := unboundedStep(status); node != nil {
		return []caseFailure{{
			step:    stepLabel(node.Step),
			field:   failureLimit,
			actual:  fmt.Sprint(node.Step.RepeatPolicy.Limit),
			message: node.Error,
		}}
	}
	if !status.Status.IsTerminal() {
		return []caseFailure{{field: failureError, actual: fmt.Sprintf("the run ended %s, which is not a terminal status", status.Status)}}
	}

	var failures []caseFailure
	if expect.Status != "" && expect.Status != status.Status.String() {
		failures = append(failures, caseFailure{field: failureStatus, expected: expect.Status, actual: status.Status.String()})
	}
	for _, s := range expect.Steps {
		node := expectedNode(status, waited, s)
		if actual := nodeStatusToken(node); s.Status != "" && s.Status != actual {
			failures = append(failures, caseFailure{step: s.Key, field: failureStatus, expected: s.Status, actual: actual})
		}
		if actual := nodeAttempts(node, s.Handler != ""); s.Attempts != nil && *s.Attempts != actual {
			failures = append(failures, caseFailure{step: s.Key, field: failureAttempts, expected: *s.Attempts, actual: actual})
		}
	}
	return failures
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
		if i := slices.IndexFunc(status.Nodes, func(n *ir.Node) bool { return n.Step.Name == s.Step }); i >= 0 {
			return status.Nodes[i]
		}
		return nil
	}
	node := handlerNode(status, s.Handler)
	for i := len(waited) - 1; i >= 0 && nodeStatusToken(node) == notStartedToken; i-- {
		node = handlerNode(waited[i], s.Handler)
	}
	return node
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
	default:
		return status.OnWait
	}
}

func nodeStatusToken(node *ir.Node) string {
	if node == nil {
		return notStartedToken
	}
	return node.Status.String()
}

// nodeAttempts returns how many times the action of a step or handler ran,
// counting retries and repeat iterations. A human.task has no action. A
// handler runs its action at most once and records no count, so one that
// started counts once.
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

// stepLabel names a step in the report: its id, or its name when it has
// none.
func stepLabel(step ir.Step) string {
	if step.ID != "" {
		return step.ID
	}
	return step.Name
}
