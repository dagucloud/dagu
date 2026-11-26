// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/untrusted"
	"github.com/stretchr/testify/require"
)

// syncLog is a step log that the test reads while the gate writes to it.
type syncLog struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (l *syncLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *syncLog) has(line string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Contains(l.buf.String(), line)
}

// gateNode returns a node of step whose stderr log the test reads.
func gateNode(step ir.Step) (*Node, *syncLog) {
	node := NewNode(step, NodeState{})
	log := &syncLog{}
	node.outputs.stderrWriter = log
	return node, log
}

// enterAsync takes a place of node in the gate and sends its release on the
// returned channel.
func enterAsync(ctx context.Context, enter func(context.Context, *Node) (func(), error), node *Node) <-chan func() {
	in := make(chan func(), 1)
	go func() {
		if leave, err := enter(ctx, node); err == nil {
			in <- leave
		}
	}()
	return in
}

// isolatedContext returns the context of a run of steps in a process with a
// restricted user.
func isolatedContext(steps ...ir.Step) context.Context {
	ctx := config.WithConfig(context.Background(), &config.Config{Untrusted: config.UntrustedConfig{Enabled: true}})
	return NewContext(ctx, &ir.DAG{Steps: steps}, "run-1", "dag.log")
}

// holdRun lets an isolated untrusted step hold the run of ctx until the test
// ends.
func holdRun(t *testing.T, ctx context.Context, step ir.Step) {
	t.Helper()
	node, _ := gateNode(step)
	leave, err := enterStepGate(ctx, node)
	require.NoError(t, err)
	t.Cleanup(leave)
}

func assertWaits(t *testing.T, in <-chan func()) {
	t.Helper()
	select {
	case <-in:
		t.Fatal("the step entered the gate while it should wait")
	case <-time.After(50 * time.Millisecond):
	}
}

// Spec 103, "Isolation", rule 1: in a run with an isolated untrusted step, the
// untrusted step waits for the running steps of its run and then holds the run
// alone. A trusted step, its preconditions included, waits for its end.
func TestStepGateOrdersIsolatedRun(t *testing.T) {
	t.Parallel()

	agentStep := ir.Step{Name: "agent", Untrusted: true}
	checkStep := ir.Step{Name: "check"}
	ctx := isolatedContext(agentStep, checkStep)

	running, _ := gateNode(checkStep)
	leaveRunning, err := enterStepGate(ctx, running)
	require.NoError(t, err)

	agent, agentLog := gateNode(agentStep)
	agentIn := enterAsync(ctx, enterStepGate, agent)
	require.Eventually(t, func() bool { return agentLog.has(untrusted.WaitingRunLine) }, 5*time.Second, 5*time.Millisecond)

	later, laterLog := gateNode(checkStep)
	laterIn := enterAsync(ctx, enterTrustedGate, later)
	require.Eventually(t, func() bool { return laterLog.has(untrusted.WaitingTrustedLine) }, 5*time.Second, 5*time.Millisecond)

	leaveRunning()
	leaveAgent := <-agentIn
	assertWaits(t, laterIn)
	leaveAgent()
	leaveLater := <-laterIn
	leaveLater()
}

// Without a restricted user, an untrusted step does not hold its run. A
// foreach step takes no place, so it does not wait for an isolated untrusted
// step: its item bodies take their own places.
func TestStepGateLetsStepsPass(t *testing.T) {
	t.Parallel()

	agentStep := ir.Step{Name: "agent", Untrusted: true}
	for name, tc := range map[string]struct {
		cfg   config.UntrustedConfig
		other ir.Step
	}{
		"no restricted user": {config.UntrustedConfig{IsolationOff: true}, ir.Step{Name: "check"}},
		"foreach step":       {config.UntrustedConfig{Enabled: true}, ir.Step{Name: "items", Foreach: &ir.ForeachConfig{}}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := config.WithConfig(context.Background(), &config.Config{Untrusted: tc.cfg})
			ctx = NewContext(ctx, &ir.DAG{Steps: []ir.Step{agentStep, tc.other}}, "run-1", "dag.log")

			agent, _ := gateNode(agentStep)
			leaveAgent, err := enterStepGate(ctx, agent)
			require.NoError(t, err)
			defer leaveAgent()
			other, _ := gateNode(tc.other)
			leaveOther, err := enterStepGate(ctx, other)
			require.NoError(t, err)
			leaveOther()
		})
	}
}

// A call or parallel step waits for an isolated untrusted step like any
// trusted step: its child runs have gates of their own, so no other step of
// the run runs while the untrusted step holds it (Spec 103, "Isolation",
// rule 1).
func TestStepGateHoldsChildRunStep(t *testing.T) {
	t.Parallel()

	agentStep := ir.Step{Name: "agent", Untrusted: true}
	for name, step := range map[string]ir.Step{
		"call":     {Name: "sub", SubDAG: &ir.SubDAG{Name: "child"}},
		"parallel": {Name: "fan", SubDAG: &ir.SubDAG{Name: "child"}, Parallel: &ir.ParallelConfig{}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := isolatedContext(agentStep, step)

			agent, _ := gateNode(agentStep)
			leaveAgent, err := enterStepGate(ctx, agent)
			require.NoError(t, err)
			sub, subLog := gateNode(step)
			subIn := enterAsync(ctx, enterStepGate, sub)
			require.Eventually(t, func() bool { return subLog.has(untrusted.WaitingTrustedLine) }, 5*time.Second, 5*time.Millisecond)

			leaveAgent()
			leaveSub := <-subIn
			leaveSub()
		})
	}
}

// A stop of the run that ends the wait of a precondition for an isolated
// untrusted step interrupts the check: the step is aborted, not failed.
func TestStepGateStopInterruptsPrecondition(t *testing.T) {
	t.Parallel()

	agentStep := ir.Step{Name: "agent", Untrusted: true}
	checkStep := ir.Step{Name: "check", Preconditions: []*ir.Condition{{Condition: "1", Expected: "1"}}}
	ctx := isolatedContext(agentStep, checkStep)
	holdRun(t, ctx, agentStep)

	check, checkLog := gateNode(checkStep)
	check.SetStatus(ir.NodeRunning)
	stepCtx, stop := context.WithCancel(WithEnv(ctx, NewEnv(ctx, checkStep)))
	defer stop()
	done := make(chan error, 1)
	go func() {
		_, err := New(&Config{}).meetsPreconditions(stepCtx, check, nil)
		done <- err
	}()
	require.Eventually(t, func() bool { return checkLog.has(untrusted.WaitingTrustedLine) }, 5*time.Second, 5*time.Millisecond)
	stop()

	require.ErrorIs(t, <-done, errConditionInterrupted)
	require.Equal(t, ir.NodeAborted, check.State().Status)
}

// A repeat condition waits for an isolated untrusted step. A step with no
// repeat condition runs no trusted code after its attempt, so it does not
// wait.
func TestStepGateHoldsRepeatCondition(t *testing.T) {
	t.Parallel()

	agentStep := ir.Step{Name: "agent", Untrusted: true}
	pollStep := ir.Step{Name: "poll", RepeatPolicy: ir.RepeatPolicy{
		RepeatMode: ir.RepeatModeUntil,
		Condition:  &ir.Condition{Condition: "1", Expected: "1"},
	}}
	onceStep := ir.Step{Name: "once"}
	ctx := isolatedContext(agentStep, pollStep, onceStep)
	holdRun(t, ctx, agentStep)
	r := New(&Config{})

	once, _ := gateNode(onceStep)
	onceDone := make(chan bool, 1)
	go func() { onceDone <- r.shouldRepeatNode(WithEnv(ctx, NewEnv(ctx, onceStep)), once, nil) }()
	select {
	case repeat := <-onceDone:
		require.False(t, repeat)
	case <-time.After(5 * time.Second):
		t.Fatal("a step with no repeat condition waited for the untrusted step")
	}

	// A stop or a timeout that ends the wait interrupts the check.
	poll, pollLog := gateNode(pollStep)
	poll.SetStatus(ir.NodeRunning)
	pollCtx, stop := context.WithCancel(WithEnv(ctx, NewEnv(ctx, pollStep)))
	defer stop()
	pollDone := make(chan bool, 1)
	go func() { pollDone <- r.shouldRepeatNode(pollCtx, poll, nil) }()
	require.Eventually(t, func() bool { return pollLog.has(untrusted.WaitingTrustedLine) }, 5*time.Second, 5*time.Millisecond)
	stop()
	require.False(t, <-pollDone)
	require.Equal(t, ir.NodeAborted, poll.State().Status)
}

// A failed capture of the outputs does not hide the error that stops the run.
func TestRunAbortedSurvivesFailedCapture(t *testing.T) {
	t.Parallel()

	executorType := registerOutputTestExecutor(t, func(ctx context.Context, exec *outputTestExecutor) error {
		_ = writeRepeatedX(ctx, exec.stdout, 4*1024)
		return fmt.Errorf("end: %w", untrusted.ErrRunAborted)
	})
	step := ir.Step{Name: "verify", Output: "RESULT", ExecutorConfig: ir.ExecutorConfig{Type: executorType}}
	ctx := NewContext(context.Background(), &ir.DAG{Name: "test", MaxOutputSize: 1024}, "run-1", "test.log")
	node := NewNode(step, NodeState{})
	require.NoError(t, node.Prepare(ctx, t.TempDir(), "run-1"))
	defer func() { _ = node.Teardown() }()

	err := node.Execute(ctx)
	require.ErrorContains(t, err, "output exceeded maximum size limit")
	require.ErrorIs(t, err, untrusted.ErrRunAborted)
}
