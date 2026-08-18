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

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/chat"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func successStep(name string, depends ...string) ir.Step {
	return newStep(name, withDepends(depends...), withCommand("exit 0"))
}

func failStep(name string, depends ...string) ir.Step {
	return newStep(name, withDepends(depends...), withCommand("exit 1"))
}

func waitStep(name string, depends ...string) ir.Step {
	return newStep(name, withDepends(depends...), withCommand("exit 0"), withApproval(&ir.ApprovalConfig{}))
}

type stepOption func(*ir.Step)

func withDepends(depends ...string) stepOption {
	return func(step *ir.Step) {
		step.Depends = depends
	}
}

func withContinueOn(c ir.ContinueOn) stepOption {
	return func(step *ir.Step) {
		step.ContinueOn = c
	}
}

func withRetryPolicy(limit int, interval time.Duration) stepOption {
	return func(step *ir.Step) {
		step.RetryPolicy.Limit = limit
		step.RetryPolicy.Interval = interval
	}
}

func withRepeatPolicy(repeat bool, interval time.Duration) stepOption {
	return func(step *ir.Step) {
		if repeat {
			step.RepeatPolicy.RepeatMode = ir.RepeatModeWhile
		}
		step.RepeatPolicy.Interval = interval
	}
}

func withPrecondition(condition *ir.Condition) stepOption {
	return func(step *ir.Step) {
		step.Preconditions = []*ir.Condition{condition}
	}
}

func withScript(script string) stepOption {
	return func(step *ir.Step) {
		step.Script = script
	}
}

func withWorkingDir(dir string) stepOption {
	return func(step *ir.Step) {
		step.Dir = dir
	}
}

func withOutput(output string) stepOption {
	return func(step *ir.Step) {
		step.Output = output
	}
}

func withStdout(stdout string) stepOption {
	return func(step *ir.Step) {
		step.Stdout = stdout
	}
}

func withEnvVars(envs ...string) stepOption {
	return func(step *ir.Step) {
		step.Env = append(step.Env, envs...)
	}
}

// parseCommand parses a command string into a CommandEntry.
func parseCommand(command string) ir.CommandEntry {
	cmd, args, err := cmdutil.SplitCommand(command)
	if err != nil {
		panic(fmt.Errorf("failed to parse command %q: %w", command, err))
	}
	return ir.CommandEntry{
		Command:     cmd,
		Args:        args,
		CmdWithArgs: command,
	}
}

func withCommand(command string) stepOption {
	return func(step *ir.Step) {
		step.Commands = []ir.CommandEntry{parseCommand(command)}
	}
}

func sequentialGuardScript(name, lockDir string) string {
	if windowsShellTest() {
		return fmt.Sprintf(`
			$lockFile = %s
			try {
				$lock = [System.IO.File]::Open($lockFile, [System.IO.FileMode]::CreateNew, [System.IO.FileAccess]::Write, [System.IO.FileShare]::None)
			} catch [System.IO.IOException] {
				Write-Error "sequential step %s overlapped another active step"
				exit 1
			}
			try {
				%s
			} finally {
				$lock.Dispose()
				Remove-Item -LiteralPath $lockFile -Force -ErrorAction SilentlyContinue
			}
		`, test.PowerShellQuote(shellTestPath(lockDir)), name,
			test.Sleep(platformTestDuration(300*time.Millisecond, 600*time.Millisecond)))
	}

	return fmt.Sprintf(`
		lock_dir=%s
		if ! mkdir "$lock_dir"; then
			echo "sequential step %s overlapped another active step" >&2
			exit 1
		fi
		trap 'rmdir "$lock_dir"' EXIT
		%s
	`, test.PosixQuote(lockDir), name, test.Sleep(300*time.Millisecond))
}

func concurrentBarrierScript(name, readyDir string, readyCount int, timeout time.Duration) string {
	timeoutSeconds := int(timeout / time.Second)
	if timeout%time.Second != 0 {
		timeoutSeconds++
	}
	if windowsShellTest() {
		return fmt.Sprintf(`
			$readyDir = %s
			New-Item -ItemType Directory -Path $readyDir -Force | Out-Null
			New-Item -ItemType File -Path (Join-Path $readyDir %s) -Force | Out-Null
			$deadline = (Get-Date).AddSeconds(%d)
			while (@(Get-ChildItem -LiteralPath $readyDir -File).Count -lt %d) {
				if ((Get-Date) -ge $deadline) {
					Write-Error "concurrent step %s did not observe all active steps"
					exit 1
				}
				Start-Sleep -Milliseconds 50
			}
		`, test.PowerShellQuote(shellTestPath(readyDir)), test.PowerShellQuote(name),
			timeoutSeconds, readyCount, name)
	}

	return fmt.Sprintf(`
		ready_dir=%s
		mkdir -p "$ready_dir"
		: > "$ready_dir/%s"
		deadline=$(( $(date +%%s) + %d ))
		while true; do
			ready_count=$(find "$ready_dir" -type f | wc -l | tr -d '[:space:]')
			if [ "$ready_count" -ge %d ]; then
				break
			fi
			if [ "$(date +%%s)" -ge "$deadline" ]; then
				echo "concurrent step %s did not observe all active steps" >&2
				exit 1
			fi
			%s
		done
	`, test.PosixQuote(readyDir), name, timeoutSeconds, readyCount, name,
		test.Sleep(50*time.Millisecond))
}

func withID(id string) stepOption {
	return func(step *ir.Step) {
		step.ID = id
	}
}

func withApproval(approval *ir.ApprovalConfig) stepOption {
	return func(step *ir.Step) {
		step.Approval = approval
	}
}

func withHumanTask(task *ir.HumanTaskConfig) stepOption {
	return func(step *ir.Step) {
		step.HumanTask = task
	}
}

func withStepTimeout(d time.Duration) stepOption {
	return func(step *ir.Step) {
		step.Timeout = d
	}
}

func newStep(name string, opts ...stepOption) ir.Step {
	step := ir.Step{Name: name}
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

func withForcedStatus(status ir.Status) runnerOption {
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

func newHandlerStep(_ *testing.T, name, id, command string) ir.Step {
	return ir.Step{
		Name:     name,
		ID:       id,
		Commands: []ir.CommandEntry{parseCommand(command)},
	}
}

func withOnSuccess(step ir.Step) runnerOption {
	return func(cfg *runtime.Config) {
		cfg.OnSuccess = &step
	}
}

func withOnFailure(step ir.Step) runnerOption {
	return func(cfg *runtime.Config) {
		cfg.OnFailure = &step
	}
}

func withOnExit(step ir.Step) runnerOption {
	return func(cfg *runtime.Config) {
		cfg.OnExit = &step
	}
}

func withOnAbort(step ir.Step) runnerOption {
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

func (th testHelper) newPlan(t *testing.T, steps ...ir.Step) planHelper {
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

func (ph planHelper) assertRun(t *testing.T, expectedStatus ir.Status) runResult {
	t.Helper()

	dag := &ir.DAG{Name: "test_dag", WorkingDir: ph.workDir}
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
	case ir.Succeeded, ir.Aborted, ir.Waiting, ir.Rejected:
		require.NoError(t, err)

	case ir.Failed, ir.PartiallySucceeded:
		require.Error(t, err)

	case ir.Running, ir.NotStarted, ir.Queued:
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

func (rr runResult) assertNodeStatus(t *testing.T, stepName string, expected ir.NodeStatus) {
	t.Helper()

	target := rr.GetNodeByName(stepName)
	if target == nil {
		if rr.cfg.OnExit != nil && rr.cfg.OnExit.Name == stepName {
			target = rr.runner.HandlerNode(ir.HandlerOnExit)
		}
		if rr.cfg.OnSuccess != nil && rr.cfg.OnSuccess.Name == stepName {
			target = rr.runner.HandlerNode(ir.HandlerOnSuccess)
		}
		if rr.cfg.OnFailure != nil && rr.cfg.OnFailure.Name == stepName {
			target = rr.runner.HandlerNode(ir.HandlerOnFailure)
		}
		if rr.cfg.OnAbort != nil && rr.cfg.OnAbort.Name == stepName {
			target = rr.runner.HandlerNode(ir.HandlerOnAbort)
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
		return rr.runner.HandlerNode(ir.HandlerOnExit)
	}
	if rr.cfg.OnSuccess != nil && rr.cfg.OnSuccess.Name == stepName {
		return rr.runner.HandlerNode(ir.HandlerOnSuccess)
	}
	if rr.cfg.OnFailure != nil && rr.cfg.OnFailure.Name == stepName {
		return rr.runner.HandlerNode(ir.HandlerOnFailure)
	}
	if rr.cfg.OnAbort != nil && rr.cfg.OnAbort.Name == stepName {
		return rr.runner.HandlerNode(ir.HandlerOnAbort)
	}

	require.FailNow(t, "step not found", "step %s not found in nodes", stepName)
	return nil
}

// mockMessagesHandler is a mock implementation of ChatMessagesHandler for testing.
type mockMessagesHandler struct {
	messages   map[string][]ir.LLMMessage
	readErr    error
	writeErr   error
	writeCalls int
}

var _ runtime.ChatMessagesHandler = (*mockMessagesHandler)(nil)

func newMockMessagesHandler() *mockMessagesHandler {
	return &mockMessagesHandler{
		messages: make(map[string][]ir.LLMMessage),
	}
}

func (m *mockMessagesHandler) ReadStepMessages(_ context.Context, stepName string) ([]ir.LLMMessage, error) {
	if m.readErr != nil {
		return nil, m.readErr
	}
	return m.messages[stepName], nil
}

func (m *mockMessagesHandler) WriteStepMessages(_ context.Context, stepName string, messages []ir.LLMMessage) error {
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
	return func(step *ir.Step) {
		step.ExecutorConfig.Type = t
	}
}

func chatStep(name string, depends ...string) ir.Step {
	return newStep(name, withDepends(depends...), withExecutorType(ir.ExecutorTypeChat))
}

// waitForNodeStatus polls until the named node reaches the given status or
// the timeout expires.
func waitForNodeStatus(plan *runtime.Plan, name string, status ir.NodeStatus, timeout time.Duration) {
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
func waitForHandlerNodeStatus(r *runtime.Runner, handler ir.HandlerType, status ir.NodeStatus, timeout time.Duration) {
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
}
