// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"fmt"
	"path"
	"syscall"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/builtin/agentstep"
	"github.com/dagucloud/dagu/internal/runtime/builtin/chat"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func successStep(name string, depends ...string) core.Step {
	return newStep(name, withDepends(depends...), withCommand("exit 0"))
}

func failStep(name string, depends ...string) core.Step {
	return newStep(name, withDepends(depends...), withCommand("exit 1"))
}

func waitStep(name string, depends ...string) core.Step {
	return newStep(name, withDepends(depends...), withCommand("exit 0"), withApproval(&core.ApprovalConfig{}))
}

type stepOption func(*core.Step)

func withDepends(depends ...string) stepOption {
	return func(step *core.Step) {
		step.Depends = depends
	}
}

func withContinueOn(c core.ContinueOn) stepOption {
	return func(step *core.Step) {
		step.ContinueOn = c
	}
}

func withRetryPolicy(limit int, interval time.Duration) stepOption {
	return func(step *core.Step) {
		step.RetryPolicy.Limit = limit
		step.RetryPolicy.Interval = interval
	}
}

func withRepeatPolicy(repeat bool, interval time.Duration) stepOption {
	return func(step *core.Step) {
		if repeat {
			step.RepeatPolicy.RepeatMode = core.RepeatModeWhile
		}
		step.RepeatPolicy.Interval = interval
	}
}

func withPrecondition(condition *core.Condition) stepOption {
	return func(step *core.Step) {
		step.Preconditions = []*core.Condition{condition}
	}
}

func withScript(script string) stepOption {
	return func(step *core.Step) {
		step.Script = script
	}
}

func withWorkingDir(dir string) stepOption {
	return func(step *core.Step) {
		step.Dir = dir
	}
}

func withOutput(output string) stepOption {
	return func(step *core.Step) {
		step.Output = output
	}
}

func withStdout(stdout string) stepOption {
	return func(step *core.Step) {
		step.Stdout = stdout
	}
}

func withEnvVars(envs ...string) stepOption {
	return func(step *core.Step) {
		step.Env = append(step.Env, envs...)
	}
}

// parseCommand parses a command string into a CommandEntry.
func parseCommand(command string) core.CommandEntry {
	cmd, args, err := cmdutil.SplitCommand(command)
	if err != nil {
		panic(fmt.Errorf("failed to parse command %q: %w", command, err))
	}
	return core.CommandEntry{
		Command:     cmd,
		Args:        args,
		CmdWithArgs: command,
	}
}

func withCommand(command string) stepOption {
	return func(step *core.Step) {
		step.Commands = []core.CommandEntry{parseCommand(command)}
	}
}

func withID(id string) stepOption {
	return func(step *core.Step) {
		step.ID = id
	}
}

func withApproval(approval *core.ApprovalConfig) stepOption {
	return func(step *core.Step) {
		step.Approval = approval
	}
}

func withStepTimeout(d time.Duration) stepOption {
	return func(step *core.Step) {
		step.Timeout = d
	}
}

func newStep(name string, opts ...stepOption) core.Step {
	step := core.Step{Name: name}
	for _, opt := range opts {
		opt(&step)
	}

	return step
}

type testHelper struct {
	test.Helper

	runner *runtime.Runner
	cfg    *runtime.Config
}

type runnerOption func(*runtime.Config)

func withTimeout(d time.Duration) runnerOption {
	return func(cfg *runtime.Config) {
		cfg.Timeout = d
	}
}

func withMaxActiveRuns(n int) runnerOption {
	return func(cfg *runtime.Config) {
		cfg.MaxActiveSteps = n
	}
}

func withForcedStatus(status core.Status) runnerOption {
	return func(cfg *runtime.Config) {
		cfg.ForcedStatus = &status
	}
}

func withDAGAutoRetry(count, limit int, isRoot bool) runnerOption {
	return func(cfg *runtime.Config) {
		cfg.DAGRunAutoRetryCount = count
		cfg.DAGRunAutoRetryLimit = limit
		cfg.DAGRunIsRoot = isRoot
	}
}

func newHandlerStep(_ *testing.T, name, id, command string) core.Step {
	return core.Step{
		Name:     name,
		ID:       id,
		Commands: []core.CommandEntry{parseCommand(command)},
	}
}

func withOnSuccess(step core.Step) runnerOption {
	return func(cfg *runtime.Config) {
		cfg.OnSuccess = &step
	}
}

func withOnFailure(step core.Step) runnerOption {
	return func(cfg *runtime.Config) {
		cfg.OnFailure = &step
	}
}

func withOnExit(step core.Step) runnerOption {
	return func(cfg *runtime.Config) {
		cfg.OnExit = &step
	}
}

func withOnAbort(step core.Step) runnerOption {
	return func(cfg *runtime.Config) {
		cfg.OnAbort = &step
	}
}

func setupRunner(t *testing.T, opts ...runnerOption) testHelper {
	t.Helper()

	th := test.Setup(t)

	cfg := &runtime.Config{
		LogDir:   th.Config.Paths.LogDir,
		DAGRunID: uuid.Must(uuid.NewV7()).String(),
	}
	for _, opt := range opts {
		opt(cfg)
	}
	r := runtime.New(cfg)

	return testHelper{
		Helper: th,
		runner: r,
		cfg:    cfg,
	}
}

func platformTestDuration(nonWindows, windows time.Duration) time.Duration {
	if windowsShellTest() {
		return windows
	}
	return nonWindows
}

func (th testHelper) newPlan(t *testing.T, steps ...core.Step) planHelper {
	t.Helper()

	plan, err := runtime.NewPlan(steps...)
	require.NoError(t, err)

	return planHelper{
		testHelper: th,
		Plan:       plan,
		workDir:    t.TempDir(),
	}
}

type planHelper struct {
	testHelper
	*runtime.Plan
	workDir string
}

func (ph planHelper) assertRun(t *testing.T, expectedStatus core.Status) runResult {
	t.Helper()

	dag := &core.DAG{Name: "test_dag", WorkingDir: ph.workDir}
	logFilename := fmt.Sprintf("%s_%s.log", dag.Name, ph.cfg.DAGRunID)
	logFilePath := path.Join(ph.cfg.LogDir, logFilename)

	ctx := runtime.NewContext(ph.Context, dag, ph.cfg.DAGRunID, logFilePath)

	var doneNodes []*runtime.Node
	progressCh := make(chan *runtime.Node)

	done := make(chan struct{})
	go func() {
		for node := range progressCh {
			doneNodes = append(doneNodes, node)
		}
		done <- struct{}{}
	}()

	err := ph.runner.Run(ctx, ph.Plan, progressCh)

	close(progressCh)

	switch expectedStatus {
	case core.Succeeded, core.Aborted, core.Waiting, core.Rejected:
		require.NoError(t, err)

	case core.Failed, core.PartiallySucceeded:
		require.Error(t, err)

	case core.Running, core.NotStarted, core.Queued:
		t.Errorf("unexpected status %s", expectedStatus)

	}

	require.Equal(t, expectedStatus.String(), ph.runner.Status(ctx, ph.Plan).String(),
		"expected status %s, got %s", expectedStatus, ph.runner.Status(ctx, ph.Plan))

	// wait for items of nodeCompletedChan to be processed
	<-done
	close(done)

	return runResult{
		planHelper: ph,
		Done:       doneNodes,
		Error:      err,
	}
}

func (ph planHelper) signal(sig syscall.Signal) {
	ph.runner.Signal(ph.Context, ph.Plan, sig, nil, false)
}

func (ph planHelper) cancel(t *testing.T) {
	t.Helper()

	ph.runner.Cancel(ph.Plan)
}

type runResult struct {
	planHelper
	Done  []*runtime.Node
	Error error
}

func (rr runResult) assertNodeStatus(t *testing.T, stepName string, expected core.NodeStatus) {
	t.Helper()

	target := rr.GetNodeByName(stepName)
	if target == nil {
		if rr.cfg.OnExit != nil && rr.cfg.OnExit.Name == stepName {
			target = rr.runner.HandlerNode(core.HandlerOnExit)
		}
		if rr.cfg.OnSuccess != nil && rr.cfg.OnSuccess.Name == stepName {
			target = rr.runner.HandlerNode(core.HandlerOnSuccess)
		}
		if rr.cfg.OnFailure != nil && rr.cfg.OnFailure.Name == stepName {
			target = rr.runner.HandlerNode(core.HandlerOnFailure)
		}
		if rr.cfg.OnAbort != nil && rr.cfg.OnAbort.Name == stepName {
			target = rr.runner.HandlerNode(core.HandlerOnAbort)
		}
	}

	require.NotNil(t, target, "step %s not found", stepName)
	require.Equal(t, expected.String(), target.State().Status.String(), "expected status %q, got %q", expected.String(), target.State().Status.String())
}

func (rr runResult) nodeByName(t *testing.T, stepName string) *runtime.Node {
	t.Helper()

	if node := rr.GetNodeByName(stepName); node != nil {
		return node
	}

	if rr.cfg.OnExit != nil && rr.cfg.OnExit.Name == stepName {
		return rr.runner.HandlerNode(core.HandlerOnExit)
	}
	if rr.cfg.OnSuccess != nil && rr.cfg.OnSuccess.Name == stepName {
		return rr.runner.HandlerNode(core.HandlerOnSuccess)
	}
	if rr.cfg.OnFailure != nil && rr.cfg.OnFailure.Name == stepName {
		return rr.runner.HandlerNode(core.HandlerOnFailure)
	}
	if rr.cfg.OnAbort != nil && rr.cfg.OnAbort.Name == stepName {
		return rr.runner.HandlerNode(core.HandlerOnAbort)
	}

	require.FailNow(t, "step not found", "step %s not found in nodes", stepName)
	return nil
}

// mockMessagesHandler is a mock implementation of ChatMessagesHandler for testing.
type mockMessagesHandler struct {
	messages   map[string][]exec.LLMMessage
	readErr    error
	writeErr   error
	writeCalls int
}

var _ runtime.ChatMessagesHandler = (*mockMessagesHandler)(nil)

func newMockMessagesHandler() *mockMessagesHandler {
	return &mockMessagesHandler{
		messages: make(map[string][]exec.LLMMessage),
	}
}

func (m *mockMessagesHandler) ReadStepMessages(_ context.Context, stepName string) ([]exec.LLMMessage, error) {
	if m.readErr != nil {
		return nil, m.readErr
	}
	return m.messages[stepName], nil
}

func (m *mockMessagesHandler) WriteStepMessages(_ context.Context, stepName string, messages []exec.LLMMessage) error {
	m.writeCalls++
	if m.writeErr != nil {
		return m.writeErr
	}
	m.messages[stepName] = messages
	return nil
}

func withMessagesHandler(handler runtime.ChatMessagesHandler) runnerOption {
	return func(cfg *runtime.Config) {
		cfg.MessagesHandler = handler
	}
}

func withExecutorType(t string) stepOption {
	return func(step *core.Step) {
		step.ExecutorConfig.Type = t
	}
}

func chatStep(name string, depends ...string) core.Step {
	return newStep(name, withDepends(depends...), withExecutorType(core.ExecutorTypeChat))
}

func agentStep(name string, depends ...string) core.Step {
	return newStep(name, withDepends(depends...), withExecutorType(core.ExecutorTypeAgent))
}

// waitForNodeStatus polls until the named node reaches the given status or
// the timeout expires.
func waitForNodeStatus(plan *runtime.Plan, name string, status core.NodeStatus, timeout time.Duration) {
	deadline := time.After(timeout)
	for {
		if node := plan.GetNodeByName(name); node != nil && node.State().Status == status {
			return
		}
		select {
		case <-deadline:
			return
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// waitForNodeDoneCount polls until the named node's DoneCount reaches at
// least the given value or the timeout expires.
func waitForNodeDoneCount(plan *runtime.Plan, name string, minCount int, timeout time.Duration) {
	_ = waitForNodeDoneCountAtLeast(plan, name, minCount, timeout)
}

func waitForNodeDoneCountAtLeast(plan *runtime.Plan, name string, minCount int, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		if node := plan.GetNodeByName(name); node != nil && node.State().DoneCount >= minCount {
			return true
		}
		select {
		case <-deadline:
			return false
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func waitForNodeRepeatScheduled(plan *runtime.Plan, name string, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		if node := plan.GetNodeByName(name); node != nil && node.State().DoneCount >= 1 && node.State().Repeated {
			return true
		}
		select {
		case <-deadline:
			return false
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// waitForHandlerNodeStatus polls until the runner's handler node for the given
// handler type reaches the specified status or the timeout expires.
func waitForHandlerNodeStatus(r *runtime.Runner, handler core.HandlerType, status core.NodeStatus, timeout time.Duration) {
	deadline := time.After(timeout)
	for {
		if node := r.HandlerNode(handler); node != nil && node.State().Status == status {
			return
		}
		select {
		case <-deadline:
			return
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func init() {
	chat.RegisterMockExecutors()
	agentstep.RegisterMockExecutors()
}
