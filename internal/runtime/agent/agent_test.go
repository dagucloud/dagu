// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/cmn/sock"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/launcher"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	profilepkg "github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/runtime/agent"
	runtimeexec "github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/runtime/runstate"
	secretpkg "github.com/dagucloud/dagu/v2/internal/secret"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
	"github.com/dagucloud/dagu/v2/internal/test"

	"github.com/stretchr/testify/require"
)

func agentCommandEntry(command string) ir.CommandEntry {
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

func setAllAgentStepCommands(dag *ir.DAG, command string) {
	entry := agentCommandEntry(command)
	for i := range dag.Steps {
		dag.Steps[i].Commands = []ir.CommandEntry{entry}
	}
}

func waitForFileScript(path string, pollInterval time.Duration) string {
	if runtime.GOOS == "windows" {
		millis := pollInterval.Milliseconds()
		if millis <= 0 {
			millis = 1
		}
		return fmt.Sprintf(`
while (-not (Test-Path %s)) {
  Start-Sleep -Milliseconds %d
}
`, test.PowerShellQuote(path), millis)
	}
	return fmt.Sprintf(`
while [ ! -f %s ]; do
  %s
done
`, test.PosixQuote(path), test.Sleep(pollInterval))
}

func signalFileThenWaitScript(signalPath, waitPath string, pollInterval time.Duration) string {
	return fmt.Sprintf("%s\n%s", writeFileCommand(signalPath, "started"), waitForFileScript(waitPath, pollInterval))
}

func writeFileCommand(path, content string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("Set-Content -Path %s -Value %s -NoNewline", test.PowerShellQuote(path), test.PowerShellQuote(content))
	}
	return fmt.Sprintf("printf '%%s' %s > %s", test.PosixQuote(content), test.PosixQuote(path))
}

func writeEnvFileCommand(path, name string) string {
	return test.ForOS(
		fmt.Sprintf("printf '%%s' \"${%s:-}\" > %s", name, test.PosixQuote(path)),
		fmt.Sprintf("Set-Content -Path %s -Value $env:%s -NoNewline", test.PowerShellQuote(path), name),
	)
}

func waitForTestFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	require.Eventually(t, func() bool {
		_, err := os.Stat(path)
		return err == nil
	}, timeout, 50*time.Millisecond)
}

func waitForCancel(t *testing.T, done <-chan struct{}, timeout time.Duration) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(timeout):
		require.FailNow(t, "timed out waiting for DAG cancellation")
	}
}

func agentRunStartTimeout() time.Duration {
	if runtime.GOOS == "windows" {
		return 30 * time.Second
	}
	return 5 * time.Second
}

// The cleanup deadline includes steps and pauses between executions.
func TestStopCleanupDeadline(t *testing.T) {
	for _, scenario := range []string{"Repeat", "RepeatInterval", "RetryInterval", "Delay"} {
		t.Run(scenario, func(t *testing.T) {
			th := test.Setup(t)
			dir := t.TempDir()
			ready := filepath.Join(dir, "ready")
			release := filepath.Join(dir, "release")
			blocking := signalFileThenWaitScript(ready, release, 50*time.Millisecond)
			yaml := fmt.Sprintf("max_clean_up_time_sec: 1\nsteps:\n  - name: probe\n    script: %q\n", blocking)
			switch scenario {
			case "Repeat":
				yaml += "    repeat_policy:\n      repeat: while\n      condition: \"true\"\n      expected: \"true\"\n"
			case "RepeatInterval":
				yaml = fmt.Sprintf("max_clean_up_time_sec: 1\nsteps:\n  - name: probe\n    script: %q\n    repeat_policy:\n      repeat: while\n      condition: \"true\"\n      expected: \"true\"\n      interval_sec: 30\n", writeFileCommand(ready, "started"))
			case "RetryInterval":
				yaml = fmt.Sprintf("max_clean_up_time_sec: 1\nsteps:\n  - name: probe\n    script: %q\n    retry_policy:\n      limit: 1\n      interval_sec: 30\n", writeFileCommand(ready, "started")+"\nexit 1")
			case "Delay":
				yaml = "delay_sec: 30\n" + yaml
			}
			dag := th.DAG(t, yaml)
			dagAgent := dag.Agent()
			done := make(chan struct{})
			go func() {
				defer close(done)
				_ = dagAgent.Run(th.Context)
			}()
			t.Cleanup(func() {
				_ = os.WriteFile(release, nil, 0600)
				dagAgent.Signal(th.Context, os.Kill)
				select {
				case <-done:
				case <-time.After(agentRunCompletionTimeout()):
					t.Error("run did not exit after releasing cleanup")
				}
			})
			waitForTestFile(t, ready, agentRunStartTimeout())
			if scenario == "RepeatInterval" || scenario == "RetryInterval" {
				require.Eventually(t, func() bool {
					node := dagAgent.Status(th.Context).Nodes[0]
					return node.DoneCount > 0 || node.RetryCount > 0
				}, agentRunStartTimeout(), 10*time.Millisecond)
			}
			go dagAgent.Signal(th.Context, os.Interrupt)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("run exceeded its cleanup deadline")
			}
			dag.AssertLatestStatus(t, ir.Aborted)
		})
	}
}

// Cleanup escalation cannot terminate an init or terminal handler.
func TestStopHandlerBudgets(t *testing.T) {
	for _, handler := range []string{"init", "abort", "exit", "timeout-exit"} {
		t.Run(handler, func(t *testing.T) {
			th := test.Setup(t)
			dir := t.TempDir()
			ready := filepath.Join(dir, "ready")
			handlerReady := filepath.Join(dir, "handler-ready")
			release := filepath.Join(dir, "release")
			finished := filepath.Join(dir, "finished")
			exited := filepath.Join(dir, "exited")
			step := signalFileThenWaitScript(ready, release, 50*time.Millisecond)
			blockedHandler := signalFileThenWaitScript(handlerReady, release, 50*time.Millisecond) + "\n" + writeFileCommand(finished, "done")
			yaml := fmt.Sprintf("max_clean_up_time_sec: 1\nsteps:\n  - script: %q\nhandler_on:\n  %s:\n    script: %q\n", step, handler, blockedHandler)
			if handler == "timeout-exit" {
				yaml = fmt.Sprintf("timeout_sec: 1\nmax_clean_up_time_sec: 1\nsteps:\n  - script: %q\nhandler_on:\n  exit:\n    script: %q\n", test.Sleep(30*time.Second), blockedHandler)
			}
			if handler != "exit" && handler != "timeout-exit" {
				yaml += fmt.Sprintf("  exit:\n    script: %q\n", writeFileCommand(exited, "done"))
			}
			dag := th.DAG(t, yaml)
			dagAgent := dag.Agent()
			done := make(chan struct{})
			go func() { defer close(done); _ = dagAgent.Run(th.Context) }()
			t.Cleanup(func() {
				_ = os.WriteFile(release, nil, 0600)
				dagAgent.Signal(th.Context, os.Kill)
				waitForCancel(t, done, agentRunCompletionTimeout())
			})
			if handler == "abort" || handler == "exit" {
				waitForTestFile(t, ready, agentRunStartTimeout())
			} else {
				waitForTestFile(t, handlerReady, agentRunStartTimeout())
			}
			go dagAgent.Signal(th.Context, os.Interrupt)
			waitForTestFile(t, handlerReady, agentRunStartTimeout())
			select {
			case <-done:
				t.Fatal("cleanup escalation terminated a lifecycle handler")
			case <-time.After(2 * time.Second):
			}
			require.NoError(t, os.WriteFile(release, nil, 0600))
			waitForCancel(t, done, agentRunCompletionTimeout())
			data, err := os.ReadFile(finished)
			require.NoError(t, err)
			require.Equal(t, "done", string(data))
			status := dagAgent.Status(th.Context)
			require.Equal(t, ir.NodeSucceeded, status.OnExit.Status)
			if handler == "init" {
				require.Equal(t, ir.NodeSucceeded, status.OnInit.Status)
				require.Equal(t, ir.NodeNotStarted, status.Nodes[0].Status)
			} else if handler == "abort" {
				require.Equal(t, ir.NodeSucceeded, status.OnAbort.Status)
			}
		})
	}
}

func TestStopConcurrentDeadline(t *testing.T) {
	th := test.Setup(t)
	ready := filepath.Join(t.TempDir(), "ready")
	dag := th.DAG(t, fmt.Sprintf(`
max_clean_up_time_sec: 3
steps:
  - script: %q
    repeat_policy:
      repeat: while
      condition: "true"
      expected: "true"
`, writeFileCommand(ready, "started")+"\n"+test.Sleep(30*time.Second)))
	dagAgent := dag.Agent()
	done := make(chan struct{})
	go func() { defer close(done); _ = dagAgent.Run(th.Context) }()
	t.Cleanup(func() {
		dagAgent.Signal(th.Context, os.Kill)
		waitForCancel(t, done, agentRunCompletionTimeout())
	})
	waitForTestFile(t, ready, agentRunStartTimeout())
	started := time.Now()
	firstStop, secondStop := make(chan struct{}), make(chan struct{})
	go func() { defer close(firstStop); dagAgent.Signal(th.Context, os.Interrupt) }()
	time.Sleep(2 * time.Second)
	go func() { defer close(secondStop); dagAgent.Signal(th.Context, os.Interrupt) }()
	waitForCancel(t, done, time.Until(started.Add(4*time.Second)))
	waitForCancel(t, firstStop, time.Second)
	waitForCancel(t, secondStop, time.Second)
	dag.AssertLatestStatus(t, ir.Aborted)
}

func agentRunCompletionTimeout() time.Duration {
	if runtime.GOOS == "windows" {
		return 3 * time.Minute
	}
	return 10 * time.Second
}

type startupBarrier struct {
	started  chan struct{}
	release  chan struct{}
	returned chan struct{}
	once     sync.Once
}

func (b *startupBarrier) wait() {
	close(b.started)
	<-b.release
	close(b.returned)
}

func (b *startupBarrier) unblock() {
	b.once.Do(func() { close(b.release) })
}

type startupSecretResolver struct{ barrier *startupBarrier }

func (r startupSecretResolver) ResolveReference(context.Context, secretref.Ref) (string, error) {
	r.barrier.wait()
	return "late-secret", nil
}

func (startupSecretResolver) CheckReferenceAccessibility(context.Context, secretref.Ref) error {
	return nil
}

type startupProfileResolver struct {
	barrier    *startupBarrier
	panicValue any
}

func (r startupProfileResolver) ResolveRuntime(context.Context, profilepkg.RuntimeRequest) (*profilepkg.RuntimeResolved, error) {
	r.barrier.wait()
	if r.panicValue != nil {
		panic(r.panicValue)
	}
	return &profilepkg.RuntimeResolved{Selected: &profilepkg.Resolved{Name: "late-profile"}}, nil
}

type startupSocketServer struct{ barrier *startupBarrier }

func (s startupSocketServer) Serve(ctx context.Context, listen chan error) error {
	close(s.barrier.started)
	<-ctx.Done()
	listen <- ctx.Err()
	close(s.barrier.returned)
	return ctx.Err()
}

func (startupSocketServer) Shutdown(context.Context) error { return nil }

// Startup termination cannot wait for a provider that ignores cancellation.
func TestStopStartup(t *testing.T) {
	for _, phase := range []string{"BeforeRun", "Dry", "Secrets", "Profile", "LatePanic", "Factory", "Socket"} {
		for _, signal := range []os.Signal{os.Interrupt, os.Kill} {
			t.Run(phase+"/"+signal.String(), func(t *testing.T) {
				th := test.Setup(t)
				marker := filepath.Join(t.TempDir(), "executed")
				script := writeFileCommand(marker, "started")
				yaml := fmt.Sprintf("max_clean_up_time_sec: 1\nsteps:\n  - script: %q\nhandler_on:\n  init:\n    script: %q\n  abort:\n    script: %q\n  exit:\n    script: %q\n", script, script, script, script)
				barrier := &startupBarrier{started: make(chan struct{}), release: make(chan struct{}), returned: make(chan struct{})}
				opts := agent.Options{Dry: phase == "Dry"}
				if phase == "Secrets" {
					yaml += "secrets:\n  - name: TOKEN\n    ref: prod/token\n"
					opts.SecretReferenceResolver = startupSecretResolver{barrier: barrier}
				} else if phase == "Profile" || phase == "LatePanic" {
					opts.ProfileName = "initial-profile"
					resolver := startupProfileResolver{barrier: barrier}
					if phase == "LatePanic" {
						resolver.panicValue = "late startup panic"
					}
					opts.ProfileResolver = resolver
				} else if phase == "Factory" {
					opts.SubWorkflowRunnerFactory = func(ctx context.Context) (runtimeexec.SubWorkflowRunner, error) {
						close(barrier.started)
						<-ctx.Done()
						close(barrier.returned)
						return nil, ctx.Err()
					}
				} else if phase == "Socket" {
					opts.SocketServerFactory = func(string, sock.HTTPHandlerFunc) (agent.SocketServer, error) {
						return startupSocketServer{barrier: barrier}, nil
					}
				}
				dag := th.DAG(t, yaml)
				dagAgent := dag.Agent(test.WithAgentOptions(opts))
				done := make(chan struct{})
				var runErr error
				t.Cleanup(func() {
					barrier.unblock()
					dagAgent.Signal(th.Context, os.Kill)
					waitForCancel(t, done, agentRunCompletionTimeout())
				})
				beforeRun := phase == "BeforeRun" || phase == "Dry"
				if beforeRun {
					dagAgent.Signal(th.Context, signal)
				}
				go func() {
					defer close(done)
					runErr = dagAgent.Run(th.Context)
				}()
				if !beforeRun {
					waitForCancel(t, barrier.started, agentRunStartTimeout())
					go dagAgent.Signal(th.Context, signal)
				}
				waitForCancel(t, done, 2*time.Second)
				require.NoError(t, runErr)
				if opts.Dry {
					dag.AssertDAGRunCount(t, 0)
				} else {
					dag.AssertLatestStatus(t, ir.Aborted)
				}
				status := dagAgent.Status(th.Context)
				require.Equal(t, ir.Aborted, status.Status)
				require.NotEmpty(t, status.FinishedAt)
				if !beforeRun {
					barrier.unblock()
					waitForCancel(t, barrier.returned, time.Second)
				}
				require.Never(t, func() bool {
					_, err := os.Stat(marker)
					return err == nil || dagAgent.Status(th.Context).ProfileName != opts.ProfileName
				}, 200*time.Millisecond, 10*time.Millisecond, "late startup completion changed an aborted run")
			})
		}
	}
}

func TestStartupPanic(t *testing.T) {
	th := test.Setup(t, test.WithCaptureLoggingOutput())
	barrier := &startupBarrier{started: make(chan struct{}), release: make(chan struct{}), returned: make(chan struct{})}
	barrier.unblock()
	dag := th.DAG(t, "steps:\n  - run: echo done\n")
	dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{ProfileResolver: startupProfileResolver{barrier: barrier, panicValue: "startup panic"}}))
	require.PanicsWithValue(t, "startup panic", func() { _ = dagAgent.Run(th.Context) })
	require.Contains(t, th.LoggingOutput.String(), "startupProfileResolver.ResolveRuntime")
}

func TestStopStartupDuplicate(t *testing.T) {
	th := test.Setup(t)
	dag := th.DAG(t, "steps:\n  - run: echo done\n")
	first := dag.Agent()
	first.RunSuccess(t)
	second := dag.Agent(test.WithDAGRunID(first.Status(th.Context).DAGRunID))
	second.Signal(th.Context, os.Interrupt)
	require.ErrorIs(t, second.Run(th.Context), dagrun.ErrDAGRunAlreadyExists)
	dag.AssertLatestStatus(t, ir.Succeeded)
}

type rejectedAttempt struct{}

func (rejectedAttempt) Error() string                 { return "attempt rejected" }
func (rejectedAttempt) AttemptRejectedReason() string { return "stale attempt" }

type rejectingStatusPusher struct {
	status   ir.NodeStatus
	rejected chan struct{}
	once     sync.Once
}

func (p *rejectingStatusPusher) Push(_ context.Context, status ir.DAGRunStatus) error {
	if len(status.Nodes) == 0 || status.Nodes[0].Status != p.status {
		return nil
	}
	var err error
	p.once.Do(func() {
		close(p.rejected)
		err = rejectedAttempt{}
	})
	return err
}

// Rejection must not block the consumer that drains runner progress and handlers.
func TestRejectedStatus(t *testing.T) {
	for _, nodeStatus := range []ir.NodeStatus{ir.NodeNotStarted, ir.NodeRunning, ir.NodeSucceeded} {
		t.Run(nodeStatus.String(), func(t *testing.T) {
			th := test.Setup(t)
			marker := filepath.Join(t.TempDir(), "exit")
			script := "echo done"
			if nodeStatus == ir.NodeRunning {
				script = test.Sleep(30 * time.Second)
			}
			dag := th.DAG(t, fmt.Sprintf("max_clean_up_time_sec: 5\nsteps:\n  - script: %q\nhandler_on:\n  abort:\n    run: echo abort\n  exit:\n    script: %q\n", script, writeFileCommand(marker, "done")))
			pusher := &rejectingStatusPusher{status: nodeStatus, rejected: make(chan struct{})}
			dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{StatusPusher: pusher}))
			done := make(chan struct{})
			go func() {
				defer close(done)
				_ = dagAgent.Run(th.Context)
			}()
			t.Cleanup(func() {
				dagAgent.Signal(th.Context, os.Kill)
				waitForCancel(t, done, agentRunCompletionTimeout())
			})
			waitForCancel(t, pusher.rejected, agentRunStartTimeout())
			waitForCancel(t, done, 3*time.Second)
			data, err := os.ReadFile(marker)
			if nodeStatus == ir.NodeNotStarted {
				require.ErrorIs(t, err, os.ErrNotExist, "handler ran before startup completed")
				require.Equal(t, ir.Aborted, dagAgent.Status(th.Context).Status)
				return
			}
			require.NoError(t, err, "exit handler was skipped after rejection")
			require.Equal(t, "done", string(data))
		})
	}
}

func TestStopSharedDeadline(t *testing.T) {
	th := test.Setup(t)
	dir := t.TempDir()
	ready, release := filepath.Join(dir, "ready"), filepath.Join(dir, "release")
	handlerReady, handlerRelease := filepath.Join(dir, "handler-ready"), filepath.Join(dir, "handler-release")
	dag := th.DAG(t, fmt.Sprintf(`
max_clean_up_time_sec: 3
steps:
  - name: probe
    script: %q
    repeat_policy:
      repeat: while
      condition: "true"
      expected: "true"
handler_on:
  exit:
    script: %q
`, signalFileThenWaitScript(ready, release, 50*time.Millisecond), signalFileThenWaitScript(handlerReady, handlerRelease, 50*time.Millisecond)))
	dagAgent := dag.Agent()
	done := make(chan struct{})
	go func() { defer close(done); _ = dagAgent.Run(th.Context) }()
	t.Cleanup(func() {
		_ = os.WriteFile(release, nil, 0600)
		_ = os.WriteFile(handlerRelease, nil, 0600)
		dagAgent.Signal(th.Context, os.Kill)
		waitForCancel(t, done, agentRunCompletionTimeout())
	})
	waitForTestFile(t, ready, agentRunStartTimeout())
	firstStop := make(chan struct{})
	started := time.Now()
	go func() { defer close(firstStop); dagAgent.Signal(th.Context, os.Interrupt) }()
	// Finishing steps releases stop callers without limiting the exit handler.
	time.Sleep(2 * time.Second)
	require.NoError(t, os.WriteFile(release, nil, 0600))
	waitForTestFile(t, handlerReady, agentRunStartTimeout())
	waitForCancel(t, firstStop, time.Second)
	secondStop := make(chan struct{})
	go func() { defer close(secondStop); dagAgent.Signal(th.Context, os.Interrupt) }()
	waitForCancel(t, secondStop, time.Second)
	time.Sleep(time.Until(started.Add(4 * time.Second)))
	dagAgent.Signal(th.Context, os.Kill)
	select {
	case <-done:
		t.Fatal("step cleanup stopped the exit handler")
	default:
	}
	require.NoError(t, os.WriteFile(handlerRelease, nil, 0600))
	waitForCancel(t, done, agentRunCompletionTimeout())
	dag.AssertLatestStatus(t, ir.Succeeded)
	require.Equal(t, ir.NodeSucceeded, dagAgent.Status(th.Context).OnExit.Status)
}

func TestStopCallerCancellation(t *testing.T) {
	for _, alreadyCanceled := range []bool{false, true} {
		t.Run(fmt.Sprintf("AlreadyCanceled=%t", alreadyCanceled), func(t *testing.T) {
			th := test.Setup(t)
			dir := t.TempDir()
			ready, release := filepath.Join(dir, "ready"), filepath.Join(dir, "release")
			dag := th.DAG(t, fmt.Sprintf(`
max_clean_up_time_sec: 2
steps:
  - name: probe
    script: %q
    repeat_policy:
      repeat: while
      condition: "true"
      expected: "true"
`, signalFileThenWaitScript(ready, release, 50*time.Millisecond)))
			dagAgent := dag.Agent()
			done := make(chan struct{})
			go func() { defer close(done); _ = dagAgent.Run(th.Context) }()
			t.Cleanup(func() {
				_ = os.WriteFile(release, nil, 0600)
				dagAgent.Signal(th.Context, os.Kill)
				waitForCancel(t, done, agentRunCompletionTimeout())
			})
			waitForTestFile(t, ready, agentRunStartTimeout())
			ctx, cancel := context.WithCancel(th.Context)
			defer cancel()
			if alreadyCanceled {
				cancel()
			}
			stopped := make(chan struct{})
			go func() { defer close(stopped); dagAgent.Signal(ctx, os.Interrupt) }()
			time.Sleep(100 * time.Millisecond)
			cancel()
			select {
			case <-done:
				t.Fatal("caller cancellation interrupted run cleanup")
			case <-time.After(100 * time.Millisecond):
			}
			require.NoError(t, os.WriteFile(release, nil, 0600))
			waitForCancel(t, done, agentRunCompletionTimeout())
			waitForCancel(t, stopped, time.Second)
			dag.AssertLatestStatus(t, ir.Succeeded)
		})
	}
}

func pwdCommand() string {
	return test.ForOS("pwd", "(Get-Location).Path")
}

type statusContextObserver struct {
	mu                          sync.Mutex
	runningStatusWritesObserved chan struct{}
	expectedDone                <-chan struct{}
	runningNodeWrites           int
	invalidContext              bool
}

func newStatusContextObserver() *statusContextObserver {
	return &statusContextObserver{runningStatusWritesObserved: make(chan struct{})}
}

func (o *statusContextObserver) observe(ctx context.Context, status ir.DAGRunStatus) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.expectedDone == nil {
		o.expectedDone = ctx.Done()
	}
	if o.expectedDone != ctx.Done() || ctx.Err() != nil {
		o.invalidContext = true
	}

	if status.Status != ir.Running {
		return
	}
	for _, node := range status.Nodes {
		if node.Status != ir.NodeRunning {
			continue
		}
		o.runningNodeWrites++
		if o.runningNodeWrites == 2 {
			// Two running-node writes confirm that progress and delayed snapshots both occurred.
			close(o.runningStatusWritesObserved)
		}
		return
	}
}

func (o *statusContextObserver) observedInvalidContext() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.invalidContext
}

type observedRunStateStore struct {
	runstate.Store
	observer *statusContextObserver
}

func (s *observedRunStateStore) BeginAttempt(
	ctx context.Context,
	req runstate.BeginAttemptRequest,
) (runstate.Attempt, error) {
	attempt, err := s.Store.BeginAttempt(ctx, req)
	if err != nil {
		return nil, err
	}
	return &observedRunStateAttempt{Attempt: attempt, observer: s.observer}, nil
}

type observedRunStateAttempt struct {
	runstate.Attempt
	observer *statusContextObserver
}

func (a *observedRunStateAttempt) RecordStatus(ctx context.Context, status ir.DAGRunStatus) error {
	a.observer.observe(ctx, status)
	return a.Attempt.RecordStatus(ctx, status)
}

type blockedOutputRunStateStore struct {
	runstate.Store
	outputStarted chan struct{}
	releaseOutput chan struct{}
}

func (s *blockedOutputRunStateStore) BeginAttempt(
	ctx context.Context,
	req runstate.BeginAttemptRequest,
) (runstate.Attempt, error) {
	attempt, err := s.Store.BeginAttempt(ctx, req)
	if err != nil {
		return nil, err
	}
	return &blockedOutputRunStateAttempt{
		Attempt:       attempt,
		outputStarted: s.outputStarted,
		releaseOutput: s.releaseOutput,
	}, nil
}

type blockedOutputRunStateAttempt struct {
	runstate.Attempt
	outputStarted     chan struct{}
	releaseOutput     <-chan struct{}
	outputStartedOnce sync.Once
}

func (a *blockedOutputRunStateAttempt) RecordOutputs(ctx context.Context, outputs *ir.DAGRunOutputs) error {
	a.outputStartedOnce.Do(func() { close(a.outputStarted) })
	select {
	case <-a.releaseOutput:
	case <-ctx.Done():
		return ctx.Err()
	}
	return a.Attempt.RecordOutputs(ctx, outputs)
}

func TestAgent_Run(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Parallel()
	}

	t.Run("RunDAG", func(t *testing.T) {
		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - run: exit 0
`)
		dagAgent := dag.Agent()

		dag.AssertLatestStatus(t, ir.NotStarted)

		runDone := make(chan error, 1)
		go func() {
			runDone <- dagAgent.Run(th.Context)
		}()

		select {
		case err := <-runDone:
			require.NoError(t, err)
		case <-time.After(agentRunCompletionTimeout()):
			t.Fatalf("timed out waiting for DAG run to finish")
		}

		dag.AssertLatestStatus(t, ir.Succeeded)
	})
	t.Run("DelayedRunningStatusWriteRemainsActiveUntilTerminalStatus", func(t *testing.T) {
		th := test.Setup(t)
		releaseFile := filepath.Join(t.TempDir(), "release")
		t.Cleanup(func() {
			_ = os.WriteFile(releaseFile, []byte("done"), 0600)
		})
		dag := th.DAG(t, fmt.Sprintf(`steps:
  - name: wait-until-released
    run: %q
`, waitForFileScript(releaseFile, 10*time.Millisecond)))

		observer := newStatusContextObserver()
		stateStore := &observedRunStateStore{
			Store:    persis.NewRunStateStore(th.DAGRunRepository, nil),
			observer: observer,
		}
		dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{
			RunStateStore:       stateStore,
			SocketServerFactory: fakeSocketServerFactory(nil),
		}))

		runDone := make(chan error, 1)
		go func() {
			runDone <- dagAgent.Run(th.Context)
		}()

		select {
		case <-observer.runningStatusWritesObserved:
		case <-time.After(agentRunStartTimeout()):
			require.FailNow(t, "timed out waiting for delayed running-status write")
		}
		require.NoError(t, os.WriteFile(releaseFile, []byte("done"), 0600))

		select {
		case err := <-runDone:
			require.NoError(t, err)
		case <-time.After(agentRunCompletionTimeout()):
			require.FailNow(t, "timed out waiting for DAG run to finish")
		}

		require.False(t, observer.observedInvalidContext())
		dag.AssertLatestStatus(t, ir.Succeeded)
	})
	for _, tc := range []struct {
		name       string
		runID      string
		command    string
		finalState ir.Status
		wantErr    bool
	}{
		{name: "Success", runID: "outputs-before-completion-success", command: `echo "result"`, finalState: ir.Succeeded},
		{name: "Failure", runID: "outputs-before-completion-failure", command: `echo "result"; exit 1`, finalState: ir.Failed, wantErr: true},
	} {
		t.Run("PersistsOutputsBeforeTerminalStatus/"+tc.name, func(t *testing.T) {
			th := test.Setup(t)
			dag := th.DAG(t, fmt.Sprintf(`steps:
  - name: emit
    run: %q
    output: RESULT
`, tc.command))
			outputStarted := make(chan struct{})
			releaseOutput := make(chan struct{})
			var releaseOutputOnce sync.Once
			release := func() { releaseOutputOnce.Do(func() { close(releaseOutput) }) }
			stateStore := &blockedOutputRunStateStore{
				Store:         persis.NewRunStateStore(th.DAGRunRepository, nil),
				outputStarted: outputStarted,
				releaseOutput: releaseOutput,
			}
			dagAgent := dag.Agent(
				test.WithDAGRunID(tc.runID),
				test.WithAgentOptions(agent.Options{
					RunStateStore:       stateStore,
					SocketServerFactory: fakeSocketServerFactory(nil),
				}),
			)

			runDone := make(chan error, 1)
			runExited := make(chan struct{})
			t.Cleanup(func() {
				release()
				select {
				case <-runExited:
				case <-time.After(agentRunCompletionTimeout()):
					t.Error("timed out waiting for DAG run cleanup")
				}
			})
			go func() {
				defer close(runExited)
				runDone <- dagAgent.Run(th.Context)
			}()

			select {
			case <-outputStarted:
			case <-time.After(agentRunCompletionTimeout()):
				t.Fatal("timed out waiting for output write")
			}

			storedAttempt, err := th.DAGRunRepository.FindAttempt(th.Context, ir.NewDAGRunRef(dag.Name, tc.runID))
			require.NoError(t, err)
			storedStatus, err := storedAttempt.ReadStatus(th.Context)
			require.NoError(t, err)
			require.Equal(t, ir.Running, storedStatus.Status)
			storedOutputs, err := storedAttempt.ReadOutputs(th.Context)
			require.NoError(t, err)
			require.Nil(t, storedOutputs)

			release()
			select {
			case err := <-runDone:
				if tc.wantErr {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
			case <-time.After(agentRunCompletionTimeout()):
				t.Fatal("timed out waiting for DAG run to finish")
			}

			storedStatus, err = storedAttempt.ReadStatus(th.Context)
			require.NoError(t, err)
			require.Equal(t, tc.finalState, storedStatus.Status)
			storedOutputs, err = storedAttempt.ReadOutputs(th.Context)
			require.NoError(t, err)
			require.NotNil(t, storedOutputs)
			require.Equal(t, dag.Name, storedOutputs.Metadata.DAGName)
			require.Equal(t, tc.runID, storedOutputs.Metadata.DAGRunID)
			require.Equal(t, storedStatus.AttemptID, storedOutputs.Metadata.AttemptID)
			require.Equal(t, storedStatus.Status.String(), storedOutputs.Metadata.Status)
			require.Equal(t, "result", storedOutputs.Outputs["result"])
		})
	}
	t.Run("RecordsTriggerActor", func(t *testing.T) {
		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - run: exit 0
`)
		dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{
			TriggerActor: "alice",
		}))

		dagAgent.RunSuccess(t)

		status := dagAgent.Status(th.Context)
		require.Equal(t, "alice", status.TriggerActor)
	})
	t.Run("HumanTaskAllowedOnRemoteWorker", func(t *testing.T) {
		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - id: review
    action: human.task
    with:
      prompt: Review the deployment
`)
		dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{WorkerID: "worker-1"}))

		err := dagAgent.Run(th.Context)
		require.NoError(t, err)
		dag.AssertLatestStatus(t, ir.Waiting)
	})
	t.Run("HumanTaskRejectedInSubDAG", func(t *testing.T) {
		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - id: review
    action: human.task
    with:
      prompt: Review the deployment
`)
		dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{
			RootDAGRun:   ir.NewDAGRunRef("root", "root-run"),
			ParentDAGRun: ir.NewDAGRunRef("parent", "parent-run"),
		}))

		err := dagAgent.Run(th.Context)
		require.ErrorContains(t, err, "cannot run as a sub-DAG")
	})
	t.Run("DeleteOldHistory", func(t *testing.T) {
		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - run: exit 0
`)
		dagAgent := dag.Agent()

		// Create a history file by running a DAG
		dagAgent.RunSuccess(t)
		dag.AssertDAGRunCount(t, 1)

		// Set the retention days to 0 (delete all history files except the latest one)
		dag.HistRetentionDays = 0

		// Run the DAG again
		dagAgent = dag.Agent()
		dagAgent.RunSuccess(t)

		// Check if only the latest history file exists
		dag.AssertDAGRunCount(t, 1)
	})
	t.Run("DeleteOldHistoryByRuns", func(t *testing.T) {
		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - run: exit 0
`)

		dag.HistRetentionRuns = 2
		dag.Agent().RunSuccess(t)
		dag.AssertDAGRunCount(t, 1)

		dag.Agent().RunSuccess(t)
		dag.AssertDAGRunCount(t, 2)

		dag.Agent().RunSuccess(t)
		dag.AssertDAGRunCount(t, 2)
	})
	t.Run("AlreadyRunning", func(t *testing.T) {
		th := test.Setup(t)
		runDir := t.TempDir()
		startedFile := filepath.Join(runDir, "started")
		releaseFile := filepath.Join(runDir, "release")
		dag := th.DAG(t, fmt.Sprintf(`steps:
  - name: wait-until-released
    run: %q
`, signalFileThenWaitScript(startedFile, releaseFile, 50*time.Millisecond)))
		dagAgent := dag.Agent(test.WithDAGRunID("test-dag-run"))
		runDone := false
		runErr := make(chan error, 1)

		go func() {
			// Run the DAG in the background so that it is running
			runErr <- dagAgent.Run(dagAgent.Context)
		}()
		t.Cleanup(func() {
			if runDone {
				return
			}
			_ = os.WriteFile(releaseFile, []byte("cleanup"), 0600)
			select {
			case <-runErr:
			case <-time.After(5 * time.Second):
				dagAgent.Abort()
			}
		})

		require.Eventually(t, func() bool {
			if _, err := os.Stat(startedFile); err != nil {
				if !os.IsNotExist(err) {
					require.NoError(t, err, "failed to stat started marker")
				}
				return false
			}
			return th.DAGRunMgr.IsRunning(context.Background(), dag.DAG, "test-dag-run")
		}, agentRunStartTimeout(), 50*time.Millisecond, "DAG should be running after the blocking step starts")

		alreadyRunningAgent := dag.Agent(test.WithDAGRunID("test-dag-run"))
		err := alreadyRunningAgent.Run(alreadyRunningAgent.Context)
		require.ErrorContains(t, err, "already running")

		require.NoError(t, os.WriteFile(releaseFile, []byte("ok"), 0600))

		// Wait for the DAG to finish
		select {
		case err := <-runErr:
			runDone = true
			require.NoError(t, err, "failed to run agent")
		case <-time.After(5 * time.Second):
			require.Fail(t, "DAG did not finish after release")
		}

		status := dagAgent.Status(context.Background())
		st := status.Status
		require.Equal(t, ir.Succeeded.String(), st.String(), "expected status %q, got %q", ir.Succeeded, st)
		for _, node := range status.Nodes {
			if node.Status == ir.NodeSkipped || node.Status == ir.NodeSucceeded {
				continue
			}
			t.Errorf("expected node %q to be in success state, got %q", node.Step.Name, node.Status.String())
		}
	})
	t.Run("RejectsAlreadyExecutedAttempt", func(t *testing.T) {
		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - run: exit 0
`)
		runID := "duplicate-attempt-run"
		prepared, err := th.DAGRunRepository.CreateAttempt(
			th.Context, dag.DAG, time.Now(), runID, persis.DAGRunCreateAttemptOptions{},
		)
		require.NoError(t, err)
		require.NoError(t, prepared.Open(th.Context))
		recorded := ir.NewStatusBuilder(dag.DAG).Create(
			runID, ir.Failed, 0, time.Now(), ir.WithAttemptID(prepared.ID()),
		)
		require.NoError(t, prepared.Write(th.Context, recorded))
		require.NoError(t, prepared.Close(th.Context))

		// The same attempt must not be executed again, and its recorded
		// status must survive; a re-run of `exit 0` would record success.
		dagAgent := dag.Agent(
			test.WithDAGRunID(runID),
			test.WithAgentOptions(agent.Options{
				RunStateStore: persis.NewRunStateStore(th.DAGRunRepository, prepared),
			}),
		)
		err = dagAgent.Run(th.Context)
		require.ErrorIs(t, err, dagrun.ErrDAGRunAlreadyExists)
		dag.AssertLatestStatus(t, ir.Failed)
	})
	t.Run("ResumesQueuedAttempt", func(t *testing.T) {
		// Queue dispatch and seeded step selections persist a queued status
		// before the agent resumes the attempt, so reopening the attempt file
		// must not be rejected as a duplicate.
		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - run: exit 0
`)
		runID := "queued-attempt-run"
		prepared, err := th.DAGRunRepository.CreateAttempt(
			th.Context, dag.DAG, time.Now(), runID, persis.DAGRunCreateAttemptOptions{},
		)
		require.NoError(t, err)
		require.NoError(t, prepared.Open(th.Context))
		queued := ir.NewStatusBuilder(dag.DAG).Create(
			runID, ir.Queued, 0, time.Time{}, ir.WithAttemptID(prepared.ID()),
		)
		require.NoError(t, prepared.Write(th.Context, queued))
		require.NoError(t, prepared.Close(th.Context))

		dagAgent := dag.Agent(
			test.WithDAGRunID(runID),
			test.WithAgentOptions(agent.Options{
				RunStateStore: persis.NewRunStateStore(th.DAGRunRepository, prepared),
			}),
		)
		dagAgent.RunSuccess(t)
		dag.AssertLatestStatus(t, ir.Succeeded)
	})
	t.Run("PreConditionNotMet", func(t *testing.T) {
		th := test.Setup(t)
		dag := th.DAG(t, fmt.Sprintf(`steps:
  - %q
  - %q
`, "exit 0", "exit 0"))

		// Set a precondition that always fails
		dag.Preconditions = []*ir.Condition{
			{Condition: "1", Expected: "0"},
		}

		dagAgent := dag.Agent()
		dagAgent.RunCancel(t)

		// Check if all nodes are not executed
		dagRunStatus := dagAgent.Status(th.Context)
		require.Equal(t, ir.Aborted.String(), dagRunStatus.Status.String())
		require.Equal(t, ir.NodeNotStarted.String(), dagRunStatus.Nodes[0].Status.String())
		require.Equal(t, ir.NodeNotStarted.String(), dagRunStatus.Nodes[1].Status.String())
	})
	t.Run("FinishWithError", func(t *testing.T) {
		th := test.Setup(t)
		errDAG := th.DAG(t, fmt.Sprintf(`steps:
  - run: %q
`, "exit 1"))
		dagAgent := errDAG.Agent()
		dagAgent.RunError(t)

		// Check if the status is saved correctly
		require.Equal(t, ir.Failed, dagAgent.Status(th.Context).Status)
	})
	t.Run("WorkDirSnapshotFailureFailsSuccessfulRun", func(t *testing.T) {
		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - run: exit 0
`)
		runID := "snapshot-failure"
		snapshotErr := errors.New("snapshot unavailable")
		workDirs := &failingWorkDirStore{dir: t.TempDir(), snapshotErr: snapshotErr}
		repository := persis.NewDAGRunRepository(
			filedagrun.NewStore(th.Config.Paths.DAGRunsDir),
			workDirs,
			persis.DAGRunRepositoryOptions{},
		)
		dagAgent := dag.Agent(
			test.WithDAGRunID(runID),
			test.WithAgentOptions(agent.Options{
				RunStateStore:       persis.NewRunStateStore(repository, nil),
				SocketServerFactory: fakeSocketServerFactory(nil),
			}),
		)

		err := dagAgent.Run(th.Context)
		require.ErrorIs(t, err, snapshotErr)
		attempt, findErr := repository.FindAttempt(th.Context, ir.NewDAGRunRef(dag.Name, runID))
		require.NoError(t, findErr)
		status, readErr := attempt.ReadStatus(th.Context)
		require.NoError(t, readErr)
		require.Equal(t, ir.Failed, status.Status)
		require.ErrorContains(t, errors.New(status.Error), "snapshot DAG-run work directory")
		require.Equal(t, 1, workDirs.snapshotCalls)
	})
	t.Run("InitFailurePersistsFinishedAt", func(t *testing.T) {
		th := test.Setup(t)
		blockingFile := filepath.Join(t.TempDir(), "not-a-dir")
		require.NoError(t, os.WriteFile(blockingFile, []byte("x"), 0600))

		dag := th.DAG(t, fmt.Sprintf(`working_dir: %q
steps:
  - run: echo hi
`, blockingFile+string(os.PathSeparator)+"subdir"))
		dagAgent := dag.Agent()

		err := dagAgent.Run(th.Context)
		require.ErrorContains(t, err, "failed to create working directory")

		latest, readErr := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
		require.NoError(t, readErr)
		require.Equal(t, ir.Failed, latest.Status)
		require.NotEmpty(t, latest.FinishedAt)
	})
	t.Run("UnsupportedSocketTransportContinuesRun", func(t *testing.T) {
		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - run: exit 0
`)
		dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{
			SocketServerFactory: fakeSocketServerFactory(
				fmt.Errorf("%w: synthetic unsupported transport", sock.ErrUnsupported),
			),
		}))

		dagAgent.RunSuccess(t)
		dag.AssertLatestStatus(t, ir.Succeeded)
	})
	t.Run("UnsupportedSocketTransportCanStopWithAbortFlag", func(t *testing.T) {
		th := test.Setup(t)
		runDir := t.TempDir()
		startedFile := filepath.Join(runDir, "started")
		releaseFile := filepath.Join(runDir, "release")
		dagRunID := "test-dag-run-no-socket-stop"
		t.Cleanup(func() {
			_ = os.WriteFile(releaseFile, []byte("cleanup"), 0600)
		})
		dag := th.DAG(t, fmt.Sprintf(`steps:
  - run: %q
`, signalFileThenWaitScript(startedFile, releaseFile, 50*time.Millisecond)))
		dagAgent := dag.Agent(
			test.WithDAGRunID(dagRunID),
			test.WithAgentOptions(agent.Options{
				SocketServerFactory: fakeSocketServerFactory(
					fmt.Errorf("%w: synthetic unsupported transport", sock.ErrUnsupported),
				),
			}),
		)
		runErr := make(chan error, 1)
		go func() {
			runErr <- dagAgent.Run(th.Context)
		}()

		waitForTestFile(t, startedFile, 2*time.Minute)
		runRef := ir.NewDAGRunRef(dag.Name, dagRunID)
		require.Eventually(t, func() bool {
			_, err := th.DAGRunRepository.FindAttempt(th.Context, runRef)
			return err == nil
		}, agentRunStartTimeout(), 100*time.Millisecond, "DAG run should be registered before stop")

		require.NoError(t, th.DAGRunMgr.Stop(th.Context, dag.DAG, dagRunID))

		select {
		case err := <-runErr:
			require.NoError(t, err)
		case <-time.After(30 * time.Second):
			require.FailNow(t, "timed out waiting for DAG run to stop via abort flag")
		}
		dag.AssertLatestStatus(t, ir.Aborted)
	})
	t.Run("SocketStartupFailureRemainsFatal", func(t *testing.T) {
		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - run: exit 0
`)
		dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{
			SocketServerFactory: fakeSocketServerFactory(errors.New("synthetic bind failure")),
		}))

		err := dagAgent.Run(th.Context)
		require.ErrorContains(t, err, "failed to start the unix socket server")
		require.ErrorContains(t, err, "synthetic bind failure")
	})
	t.Run("FailureHandlerRunsInlineWithoutDAGAutoRetry", func(t *testing.T) {
		th := test.Setup(t)
		marker := filepath.Join(t.TempDir(), "failure-marker")
		dag := th.DAG(t, fmt.Sprintf(`retry_policy:
  limit: 0
handler_on:
  failure:
    run: %q
steps:
  - %q
`, writeFileCommand(marker, "failed"), "exit 1"))
		dagAgent := dag.Agent()
		dagAgent.RunError(t)

		status := dagAgent.Status(th.Context)
		require.Equal(t, ir.Failed, status.Status)
		require.NotNil(t, status.OnFailure)
		require.Equal(t, ir.NodeSucceeded, status.OnFailure.Status)
		require.NotEmpty(t, status.StartedAt)
		require.NotEmpty(t, status.FinishedAt)

		data, err := os.ReadFile(marker)
		require.NoError(t, err)
		require.Equal(t, "failed", string(data))
	})
	t.Run("FailureHandlerSkipsRootDAGPendingAutoRetry", func(t *testing.T) {
		th := test.Setup(t)
		marker := filepath.Join(t.TempDir(), "failure-marker")
		dag := th.DAG(t, fmt.Sprintf(`retry_policy:
  limit: 1
handler_on:
  failure:
    run: %q
steps:
  - %q
`, writeFileCommand(marker, "failed"), "exit 1"))
		dagAgent := dag.Agent()
		dagAgent.RunError(t)

		status := dagAgent.Status(th.Context)
		require.Equal(t, ir.Failed, status.Status)
		require.NotNil(t, status.OnFailure)
		require.Equal(t, ir.NodeNotStarted, status.OnFailure.Status)

		_, err := os.Stat(marker)
		require.ErrorIs(t, err, os.ErrNotExist)
	})
	t.Run("FinishWithTimeout", func(t *testing.T) {
		th := test.Setup(t)
		timeoutDAG := th.DAG(t, fmt.Sprintf(`timeout_sec: 2
steps:
  - %q
  - %q
`, test.Sleep(time.Second), test.Sleep(2*time.Second)))
		dagAgent := timeoutDAG.Agent()
		dagAgent.RunError(t)

		// Check if the status is saved correctly
		require.Equal(t, ir.Failed, dagAgent.Status(th.Context).Status)
	})
	t.Run("ReceiveSignal", func(t *testing.T) {
		th := test.Setup(t)
		releaseFile := filepath.Join(t.TempDir(), "release")
		dagRunID := "test-dag-run-receive-signal"
		t.Cleanup(func() {
			_ = os.WriteFile(releaseFile, []byte("ok"), 0600)
		})
		dag := th.DAG(t, fmt.Sprintf(`steps:
  - %q
`, waitForFileScript(releaseFile, 50*time.Millisecond)))
		dagAgent := dag.Agent(test.WithDAGRunID(dagRunID))
		done := make(chan struct{})

		go func() {
			defer close(done)
			dagAgent.RunCancel(t)
		}()

		require.Eventually(t, func() bool {
			status, err := th.DAGRunMgr.GetCurrentStatus(context.Background(), dag.DAG, dagRunID)
			if err != nil || status == nil || status.Status != ir.Running {
				return false
			}
			return th.DAGRunMgr.IsRunning(context.Background(), dag.DAG, dagRunID)
		}, 2*time.Minute, 250*time.Millisecond, "expected DAG to reach running state before abort")

		// send a signal to cancel the DAG
		dagAgent.Abort()

		waitForCancel(t, done, 30*time.Second)

		// wait for the DAG to be canceled
		dag.AssertLatestStatus(t, ir.Aborted)
	})
	t.Run("ExitHandler", func(t *testing.T) {
		th := test.Setup(t)
		dag := th.DAG(t, fmt.Sprintf(`handler_on:
  exit:
    run: %q
steps:
  - %q
  - %q
`, "exit 0", "exit 0", "exit 0"))
		dagAgent := dag.Agent()
		dagAgent.RunSuccess(t)

		// Check if the DAG is executed successfully
		dagRunStatus := dagAgent.Status(th.Context)
		require.Equal(t, ir.Succeeded.String(), dagRunStatus.Status.String())
		for _, s := range dagRunStatus.Nodes {
			require.Equal(t, ir.NodeSucceeded.String(), s.Status.String())
		}

		// Check if the exit handler is executed
		require.Equal(t, ir.NodeSucceeded.String(), dagRunStatus.OnExit.Status.String())
	})
}

func TestAgent_WorkingDirExpansion(t *testing.T) {
	t.Run("WorkingDirWithEnvVar", func(t *testing.T) {
		th := test.Setup(t)
		// Set up a temp directory and env var
		tempDir := t.TempDir()
		t.Setenv("TEST_WORK_DIR", tempDir)

		// Create DAG with WorkingDir using env var
		dag := th.DAG(t, `working_dir: $TEST_WORK_DIR
steps:
  - name: check-pwd
    run: `+pwdCommand()+`
`)
		dagAgent := dag.Agent()
		dagAgent.RunSuccess(t)

		// Verify the DAG ran successfully
		dagRunStatus := dagAgent.Status(th.Context)
		require.Equal(t, ir.Succeeded.String(), dagRunStatus.Status.String())
	})

	t.Run("WorkingDirWithDAGEnvVar", func(t *testing.T) {
		th := test.Setup(t)
		tempDir := t.TempDir()
		origWD, err := os.Getwd()
		require.NoError(t, err)
		t.Cleanup(func() {
			_ = os.Chdir(origWD)
		})

		// Create DAG with WorkingDir using DAG-defined env var
		dag := th.DAG(t, `env:
  - CUSTOM_DIR=`+tempDir+`
working_dir: $CUSTOM_DIR
steps:
  - name: check-pwd
    run: `+pwdCommand()+`
`)
		dagAgent := dag.Agent()
		dagAgent.RunSuccess(t)

		// Verify the DAG ran successfully
		dagRunStatus := dagAgent.Status(th.Context)
		require.Equal(t, ir.Succeeded.String(), dagRunStatus.Status.String())
	})

	t.Run("WorkingDirWithTildeExpansion", func(t *testing.T) {
		th := test.Setup(t)

		// Create DAG with WorkingDir using tilde
		dag := th.DAG(t, `working_dir: ~
steps:
  - name: check-pwd
    run: `+pwdCommand()+`
`)
		dagAgent := dag.Agent()
		dagAgent.RunSuccess(t)

		// Verify the DAG ran successfully
		dagRunStatus := dagAgent.Status(th.Context)
		require.Equal(t, ir.Succeeded.String(), dagRunStatus.Status.String())
	})
}

func TestAgent_DryRun(t *testing.T) {
	t.Run("DryRun", func(t *testing.T) {
		th := test.Setup(t)

		dag := th.DAG(t, fmt.Sprintf(`steps:
  - %q
`, "exit 0"))
		dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{Dry: true}))

		dagAgent.RunSuccess(t)

		curStatus := dagAgent.Status(th.Context)
		require.Equal(t, ir.Succeeded, curStatus.Status)

		// Check if the status is not saved
		dag.AssertDAGRunCount(t, 0)
	})
}

func TestAgent_Retry(t *testing.T) {
	t.Parallel()

	t.Run("RetryDAG", func(t *testing.T) {
		th := test.Setup(t)
		// retry DAG that fails
		dag := th.DAG(t, fmt.Sprintf(`type: graph
steps:
  - name: "1"
    run: %q
  - name: "2"
    run: %q
    continue_on:
      failure: true
    depends: ["1"]
  - name: "3"
    run: %q
    depends: ["2"]
  - name: "4"
    run: %q
    preconditions:
      - condition: "`+"`"+`echo 0`+"`"+`"
        expected: "1"
    continue_on:
      skipped: true
  - name: "5"
    run: %q
    depends: ["4"]
  - name: "6"
    run: %q
    depends: ["5"]
  - name: "7"
    run: %q
    preconditions:
      - condition: "`+"`"+`echo 0`+"`"+`"
        expected: "1"
    depends: ["6"]
    continue_on:
      skipped: true
  - name: "8"
    run: %q
    preconditions:
      - condition: "`+"`"+`echo 0`+"`"+`"
        expected: "1"
  - name: "9"
    run: %q
`, "exit 0", "exit 1", "exit 0", "exit 0", "exit 1", "exit 0", "exit 0", "exit 0", "exit 1"))
		dagAgent := dag.Agent()

		dagAgent.RunError(t)
		require.Equal(t, 0, dagAgent.Status(th.Context).AutoRetryCount)

		// Modify the DAG to make it successful
		dagRunStatus := dagAgent.Status(th.Context)
		setAllAgentStepCommands(dag.DAG, "exit 0")

		// Retry the DAG and check if it is successful
		dagAgent = dag.Agent(test.WithAgentOptions(agent.Options{
			RetryTarget: &dagRunStatus,
		}))
		dagAgent.RunSuccess(t)
		require.Equal(t, 0, dagAgent.Status(th.Context).AutoRetryCount)

		for _, node := range dagAgent.Status(th.Context).Nodes {
			if node.Status != ir.NodeSucceeded &&
				node.Status != ir.NodeSkipped {
				t.Errorf("node %q is not successful: %s", node.Step.Name, node.Status)
			}
		}
	})

	t.Run("StepRetry", func(t *testing.T) {
		th := test.Setup(t)
		dag := th.DAG(t, fmt.Sprintf(`type: graph
steps:
  - name: "1"
    run: %q
  - name: "2"
    run: %q
    continue_on:
      failure: true
    depends: ["1"]
  - name: "3"
    run: %q
    depends: ["2"]
  - name: "4"
    run: %q
    preconditions:
      - condition: "`+"`"+`echo 0`+"`"+`"
        expected: "1"
    continue_on:
      skipped: true
  - name: "5"
    run: %q
    depends: ["4"]
  - name: "6"
    run: %q
    depends: ["5"]
  - name: "7"
    run: %q
    preconditions:
      - condition: "`+"`"+`echo 0`+"`"+`"
        expected: "1"
    depends: ["6"]
    continue_on:
      skipped: true
  - name: "8"
    run: %q
    preconditions:
      - condition: "`+"`"+`echo 0`+"`"+`"
        expected: "1"
  - name: "9"
    run: %q
`, "exit 0", "exit 1", "exit 0", "exit 0", "exit 1", "exit 0", "exit 0", "exit 0", "exit 1"))
		dagAgent := dag.Agent()

		// Run the DAG to get a failed status
		dagAgent.RunError(t)
		dagRunStatus := dagAgent.Status(th.Context)

		// Save FinishedAt for all nodes before retry
		prevFinishedAt := map[string]string{}
		for _, node := range dagRunStatus.Nodes {
			prevFinishedAt[node.Step.Name] = node.FinishedAt
		}

		// Modify the DAG to make all steps successful
		setAllAgentStepCommands(dag.DAG, "exit 0")

		// Wait until the current time (RFC3339, second precision) differs
		// from the previous FinishedAt timestamps so that retried steps
		// are guaranteed to have a newer formatted timestamp.
		prev := time.Now().Format(time.RFC3339)
		for time.Now().Format(time.RFC3339) == prev {
			time.Sleep(10 * time.Millisecond)
		}

		// Retry from step '5' using StepRetry
		dagAgent = dag.Agent(test.WithAgentOptions(agent.Options{
			RetryTarget: &dagRunStatus,
			StepRetry:   "5",
		}))
		err := dagAgent.Run(context.Background())
		require.NoError(t, err)

		// Only node 5 is retried, downstream steps remain untouched
		retried := map[string]struct{}{"5": {}}
		// Node 2 is a false command and should remain failed
		// Downstream nodes (6, 7, 8, 9) should remain in their previous state
		falseSteps := map[string]struct{}{"2": {}}
		// Check that only step '5' is rerun, all other steps remain unchanged
		st := dagAgent.Status(th.Context)

		for _, node := range st.Nodes {
			name := node.Step.Name
			prev := prevFinishedAt[name]
			now := node.FinishedAt

			if _, isRetried := retried[name]; isRetried {
				// Only step '5' should be retried and successful
				if node.Status != ir.NodeSucceeded && node.Status != ir.NodeSkipped {
					t.Errorf("step %q is not successful or skipped after step retry: %s", name, node.Status)
				}
				// FinishedAt should be fresher (more recent) than before, if it was set
				if prev != "" && now != "" && now <= prev {
					t.Errorf("retried step %q FinishedAt not updated: was %v, now %v", name, prev, now)
				}
			} else {
				// Assert that steps with "false" commands are still failed
				if _, isFalseStep := falseSteps[name]; isFalseStep {
					if node.Status != ir.NodeFailed {
						t.Errorf("non-retried step %q (false command) should remain failed after step retry, got: %s", name, node.Status)
					}
				}
				// FinishedAt should be unchanged for all non-retried steps
				if prev != now {
					t.Errorf("non-retried step %q FinishedAt changed after step retry: was %v, now %v", name, prev, now)
				}
			}
		}
	})
}

func TestAgent_HandleHTTP(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Parallel()
	}

	t.Run("HTTPValid", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Parallel()
		}
		th := test.Setup(t)

		tmpDir := t.TempDir()
		releaseFile := filepath.Join(tmpDir, "http-valid.release")
		startedFile := filepath.Join(tmpDir, "http-valid.started")
		t.Cleanup(func() {
			_ = os.WriteFile(releaseFile, []byte("ok"), 0600)
		})
		dag := th.DAG(t, fmt.Sprintf(`steps:
  - run: %q
`, writeFileCommand(startedFile, "started")+"\n"+waitForFileScript(releaseFile, 50*time.Millisecond)))
		dagAgent := dag.Agent()
		ctx := th.Context
		done := make(chan struct{})
		go func() {
			defer close(done)
			dagAgent.RunCancel(t)
		}()

		waitForTestFile(t, startedFile, 2*time.Minute)

		// Get the status of the DAG
		require.Eventually(t, func() bool {
			rw := mockResponseWriter{}
			dagAgent.HandleHTTP(ctx)(&rw, &http.Request{
				Method: "GET", URL: &url.URL{Path: "/status"},
			})
			if rw.status != http.StatusOK {
				return false
			}

			dagRunStatus, err := ir.StatusFromJSON(rw.body)
			return err == nil && dagRunStatus.Status == ir.Running
		}, 10*time.Second, 50*time.Millisecond)

		// Stop the DAG
		dagAgent.Abort()

		waitForCancel(t, done, 30*time.Second)
		dag.AssertLatestStatus(t, ir.Aborted)
	})
	t.Run("HTTPInvalidRequest", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Parallel()
		}
		th := test.Setup(t)

		tmpDir := t.TempDir()
		releaseFile := filepath.Join(tmpDir, "http-invalid.release")
		startedFile := filepath.Join(tmpDir, "http-invalid.started")
		t.Cleanup(func() {
			_ = os.WriteFile(releaseFile, []byte("ok"), 0600)
		})
		dag := th.DAG(t, fmt.Sprintf(`steps:
  - run: %q
`, writeFileCommand(startedFile, "started")+"\n"+waitForFileScript(releaseFile, 50*time.Millisecond)))
		dagAgent := dag.Agent()

		done := make(chan struct{})
		go func() {
			defer close(done)
			dagAgent.RunCancel(t)
		}()

		waitForTestFile(t, startedFile, 2*time.Minute)

		rw := mockResponseWriter{}

		// Request with an invalid path
		dagAgent.HandleHTTP(th.Context)(&rw, &http.Request{
			Method: "GET",
			URL:    &url.URL{Path: "/invalid-path"},
		})
		require.Equal(t, http.StatusNotFound, rw.status)

		// Stop the DAG
		dagAgent.Abort()
		waitForCancel(t, done, 30*time.Second)
		dag.AssertLatestStatus(t, ir.Aborted)
	})
	t.Run("HTTPHandleCancel", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Parallel()
		}
		th := test.Setup(t)

		tmpDir := t.TempDir()
		releaseFile := filepath.Join(tmpDir, "http-cancel.release")
		startedFile := filepath.Join(tmpDir, "http-cancel.started")
		t.Cleanup(func() {
			_ = os.WriteFile(releaseFile, []byte("ok"), 0600)
		})
		dag := th.DAG(t, fmt.Sprintf(`steps:
  - run: %q
`, writeFileCommand(startedFile, "started")+"\n"+waitForFileScript(releaseFile, 50*time.Millisecond)))
		dagAgent := dag.Agent()

		done := make(chan struct{})
		go func() {
			defer close(done)
			dagAgent.RunCancel(t)
		}()

		waitForTestFile(t, startedFile, 2*time.Minute)

		// Cancel the DAG
		rw := mockResponseWriter{}
		dagAgent.HandleHTTP(th.Context)(&rw, &http.Request{
			Method: "POST",
			URL:    &url.URL{Path: "/stop"},
		})
		require.Equal(t, http.StatusOK, rw.status)
		require.Equal(t, "OK", rw.body)

		// Wait for the DAG to stop
		waitForCancel(t, done, 30*time.Second)
		dag.AssertLatestStatus(t, ir.Aborted)
	})
}

func TestAgent_SubDAGSocketUsesCurrentRunIdentity(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	dag := th.DAG(t, `name: child-dag
steps:
  - run: exit 0
`)
	const childRunID = "child-run"
	bindErr := errors.New("stop after capturing socket address")
	var socketAddr string
	dagAgent := agent.New(
		childRunID,
		dag.DAG,
		th.Config.Paths.LogDir,
		filepath.Join(th.Config.Paths.LogDir, childRunID+".log"),
		th.DAGRunMgr,
		th.DAGRepository,
		agent.Options{
			RootDAGRun:   ir.NewDAGRunRef("root-dag", "root-run"),
			ParentDAGRun: ir.NewDAGRunRef("root-dag", "root-run"),
			SocketServerFactory: func(addr string, _ sock.HTTPHandlerFunc) (agent.SocketServer, error) {
				socketAddr = addr
				return nil, bindErr
			},
		},
	)

	err := dagAgent.Run(th.Context)

	require.ErrorIs(t, err, bindErr)
	require.Equal(t, sock.Addr("child-dag", childRunID), socketAddr)
}

func TestAgentUsesProvidedWorkDir(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	workDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "input.txt"), []byte("input"), 0o600))
	dag := th.DAG(t, `name: workspace-dag
steps:
  - run: test -f "$DAG_RUN_WORK_DIR/input.txt"
`)

	dag.Agent(test.WithAgentOptions(agent.Options{WorkDir: workDir})).RunSuccess(t)
}

func TestAgentMaterializesLocalFileDependencies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX shell commands")
	}
	t.Parallel()

	th := test.Setup(t)
	sourceDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "input.txt"), []byte("input"), 0o600))
	dag := th.DAG(t, fmt.Sprintf(`
name: local-workspace
env:
  SOURCE_DIR: %q
working_dir: $SOURCE_DIR
steps:
  - id: consume
    run: |
      test -f input.txt
      test "$PWD" != %s
    dependencies: input.txt
`, sourceDir, test.PosixQuote(sourceDir)))

	dag.Agent().RunSuccess(t)
}

// Assert that mockResponseWriter implements http.ResponseWriter
var _ http.ResponseWriter = (*mockResponseWriter)(nil)

type mockResponseWriter struct {
	status int
	body   string
	header *http.Header
}

type fakeSocketServer struct {
	serveErr error
}

// fakeSocketServerFactory returns a socket factory with deterministic serve behavior.
func fakeSocketServerFactory(serveErr error) agent.SocketServerFactory {
	return func(string, sock.HTTPHandlerFunc) (agent.SocketServer, error) {
		return &fakeSocketServer{serveErr: serveErr}, nil
	}
}

// Serve reports the configured startup result through the listen channel.
func (s *fakeSocketServer) Serve(_ context.Context, listen chan error) error {
	if listen != nil {
		listen <- s.serveErr
	}
	return s.serveErr
}

// Shutdown is a no-op for the fake socket server.
func (*fakeSocketServer) Shutdown(context.Context) error {
	return nil
}

func (h *mockResponseWriter) Header() http.Header {
	if h.header == nil {
		h.header = &http.Header{}
	}
	return *h.header
}

func (h *mockResponseWriter) Write(body []byte) (int, error) {
	h.body = string(body)
	return len([]byte(h.body)), nil
}

func (h *mockResponseWriter) WriteHeader(statusCode int) {
	h.status = statusCode
}

func TestAgent_OutputCollection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		dag      string
		expected map[string]string
	}{
		{
			name: "SimpleOutput",
			dag: `steps:
  - name: step1
    run: echo "hello"
    output: RESULT`,
			expected: map[string]string{"result": "hello"},
		},
		{
			name: "CamelCaseConversion",
			dag: `steps:
  - name: step1
    run: echo "value"
    output: MY_OUTPUT_VAR`,
			expected: map[string]string{"myOutputVar": "value"},
		},
		{
			name: "StructuredOutputParticipates",
			dag: `steps:
  - id: publish
    output:
      label: "value"`,
			expected: map[string]string{"label": "value"},
		},
		{
			name: "MultipleSteps",
			dag: `steps:
  - name: step1
    run: echo "one"
    output: OUTPUT_ONE
  - name: step2
    run: echo "two"
    output: OUTPUT_TWO`,
			expected: map[string]string{"outputOne": "one", "outputTwo": "two"},
		},
		{
			name: "LastOneWins",
			dag: `type: graph
steps:
  - name: step1
    run: echo "first"
    output: RESULT
  - name: step2
    run: echo "second"
    output: RESULT
    depends: [step1]`,
			expected: map[string]string{"result": "second"},
		},
		{
			name: "NoOutputs",
			dag: fmt.Sprintf(`steps:
  - name: step1
    run: %q`, "exit 0"),
			expected: map[string]string{},
		},
		{
			name: "LifecycleHandlerOutputs",
			dag: `handler_on:
  exit:
    run: echo '{"value":"from-handler"}'
    stdout:
      outputs:
        fields:
          from_handler:
            decode: json
            select: .value
steps:
  - name: step1
    run: echo '{"value":"from-step"}'
    stdout:
      outputs:
        fields:
          from_step:
            decode: json
            select: .value`,
			expected: map[string]string{"from_step": "from-step", "from_handler": "from-handler"},
		},
		{
			name: "LifecycleHandlerOutputVariable",
			dag: `handler_on:
  exit:
    run: echo "done"
    output: HANDLER_RESULT
steps:
  - name: step1
    run: echo "one"
    output: OUTPUT_ONE`,
			expected: map[string]string{"outputOne": "one", "handlerResult": "done"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			th := test.Setup(t)
			dag := th.DAG(t, tc.dag)
			dagAgent := dag.Agent()
			dagAgent.RunSuccess(t)

			outputs := dag.ReadOutputs(t)
			for k, v := range tc.expected {
				require.Equal(t, v, outputs[k], "output %s mismatch", k)
			}
			require.Len(t, outputs, len(tc.expected))
		})
	}
}

func TestAgent_OutputSecretMasking(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)

	secretValue := "super-secret-token-xyz123"
	secretFile := th.TempFile(t, "secret.txt", []byte(secretValue))

	dag := th.DAG(t, `
secrets:
  - name: API_TOKEN
    provider: file
    key: `+secretFile+`
steps:
  - name: step1
    run: echo "Token is ${API_TOKEN}"
    output: RESPONSE`)

	dagAgent := dag.Agent()
	dagAgent.RunSuccess(t)

	outputs := dag.ReadOutputs(t)
	require.NotContains(t, outputs["response"], secretValue, "secret should be masked")
	require.Contains(t, outputs["response"], "*******", "masked placeholder expected")
}

// A later step of the same run reads a published output with the secret
// intact, while stored run status keeps only the mask.
func TestAgent_StepOutputSecretMasking(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX shell commands")
	}
	t.Parallel()
	th := test.Setup(t)

	secretValue := "step-output-secret-5b7e"
	secretFile := th.TempFile(t, "secret.txt", []byte(secretValue))

	dag := th.DAG(t, `type: graph
secrets:
  - name: API_TOKEN
    provider: file
    key: `+secretFile+`
steps:
  - id: login
    run: printf 'token=%s\n' "$API_TOKEN" >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: token
  - id: use
    depends: [login]
    run: test '${steps.login.outputs.token}' = "$API_TOKEN"
`)
	dag.Agent().RunSuccess(t)

	latest, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	login, err := latest.NodeByName("login")
	require.NoError(t, err)
	require.NotNil(t, login.StepOutputsValue)
	require.JSONEq(t, `{"token":"*******"}`, *login.StepOutputsValue)

	statusJSON, err := json.Marshal(latest)
	require.NoError(t, err)
	require.NotContains(t, string(statusJSON), secretValue)
}

func TestAgent_RegistryRefSecretResolution(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)

	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)
	secretStore, err := store.NewSecretStore(testutil.NewMemoryBackend().Collection("secrets"), enc)
	require.NoError(t, err)

	sec, err := secretpkg.New(secretpkg.CreateInput{
		Ref:          "prod/db-password",
		ProviderType: secretpkg.ProviderDaguManaged,
		CreatedBy:    "alice",
	}, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, secretStore.Create(context.Background(), sec, &secretpkg.WriteValueInput{
		Value:     "managed-secret",
		CreatedBy: "alice",
		CreatedAt: time.Now().UTC(),
	}))

	dag := th.DAG(t, `
secrets:
  - name: DB_PASSWORD
    ref: prod/db-password
steps:
  - name: step1
    run: test "${DB_PASSWORD}" = "managed-secret" && echo ok
    output: RESPONSE`)

	dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{SecretStore: secretStore}))
	dagAgent.RunSuccess(t)

	outputs := dag.ReadOutputs(t)
	require.Equal(t, "ok", outputs["response"])
}

func TestAgent_RuntimeProfileInjection(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)

	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)
	secretStore, err := store.NewSecretStore(testutil.NewMemoryBackend().Collection("secrets"), enc)
	require.NoError(t, err)
	profileStore, err := store.NewProfileStore(testutil.NewMemoryBackend().Collection("profiles"))
	require.NoError(t, err)

	sec, err := secretpkg.New(secretpkg.CreateInput{
		Ref:          "prod/api-token",
		ProviderType: secretpkg.ProviderDaguManaged,
		CreatedBy:    "alice",
	}, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, secretStore.Create(context.Background(), sec, &secretpkg.WriteValueInput{
		Value:     "managed-secret",
		CreatedBy: "alice",
		CreatedAt: time.Now().UTC(),
	}))

	prof, err := profilepkg.New(profilepkg.CreateInput{
		Name:      "prod",
		CreatedBy: "alice",
	}, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, prof.SetVariable("LOG_LEVEL", "debug", "alice", time.Now().UTC()))
	require.NoError(t, prof.SetSecret("API_TOKEN", sec.ID, "alice", time.Now().UTC()))
	require.NoError(t, profileStore.Create(context.Background(), prof))

	dag := th.DAG(t, `
steps:
  - name: step1
    run: echo "Level is ${LOG_LEVEL}; token is ${API_TOKEN}"
    output: RESPONSE`)

	dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{
		ProfileStore: profileStore,
		SecretStore:  secretStore,
		ProfileName:  "prod",
	}))
	dagAgent.RunSuccess(t)

	outputs := dag.ReadOutputs(t)
	require.Contains(t, outputs["response"], "Level is debug")
	require.NotContains(t, outputs["response"], "managed-secret")
	require.Contains(t, outputs["response"], "*******")

	status := dagAgent.Status(th.Context)
	require.Equal(t, "prod", status.ProfileName)
	require.NotEmpty(t, status.ProfileResolvedAt)
	require.ElementsMatch(t, []ir.RuntimeProfileEntry{
		{Key: "LOG_LEVEL", Kind: "variable"},
		{Key: "API_TOKEN", Kind: "secret"},
	}, status.ProfileEntries)

	statusJSON, err := json.Marshal(status)
	require.NoError(t, err)
	require.NotContains(t, string(statusJSON), "managed-secret")
	require.Contains(t, string(statusJSON), "*******")

	latest, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	latestStatusJSON, err := json.Marshal(latest)
	require.NoError(t, err)
	require.NotContains(t, string(latestStatusJSON), "managed-secret")
	require.Contains(t, string(latestStatusJSON), "*******")
}

func TestAgent_RuntimeConfigVarsUseRuntimeProfilePrecedence(t *testing.T) {
	t.Parallel()

	vars := agent.RuntimeConfigVarsForTest(
		[]string{
			"DAG_BEATS_DEFAULT=global",
			"DEFAULT_ONLY=global",
		},
		[]string{
			"DAG_BEATS_DEFAULT_SECRET=global-secret",
			"DEFAULT_SECRET=global-secret",
			"SECRET_SHARED=global-secret",
		},
		[]string{
			"DAG_BEATS_DEFAULT=dag",
			"DAG_BEATS_DEFAULT_SECRET=dag",
			"SELECTED_BEATS_DAG=dag",
			"SECRET_SHARED=dag-env",
		},
		[]string{
			"SELECTED_BEATS_DAG=selected",
			"SELECTED_ONLY=selected",
		},
		[]string{
			"SECRET_SHARED=selected-secret",
		},
		[]string{
			"SECRET_SHARED=dag-secret",
			"DAG_SECRET=dag-secret",
		},
	)

	require.Equal(t, "dag", vars["DAG_BEATS_DEFAULT"])
	require.Equal(t, "dag", vars["DAG_BEATS_DEFAULT_SECRET"])
	require.Equal(t, "global", vars["DEFAULT_ONLY"])
	require.Equal(t, "global-secret", vars["DEFAULT_SECRET"])
	require.Equal(t, "selected", vars["SELECTED_BEATS_DAG"])
	require.Equal(t, "selected", vars["SELECTED_ONLY"])
	require.Equal(t, "dag-secret", vars["SECRET_SHARED"])
	require.Equal(t, "dag-secret", vars["DAG_SECRET"])
}

func TestAgent_LayeredRuntimeProfiles(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)

	backend := testutil.NewMemoryBackend()
	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)
	secretStore, err := store.NewSecretStore(backend.Collection("secrets"), enc)
	require.NoError(t, err)
	profileStore, err := store.NewProfileStore(backend.Collection("profiles"))
	require.NoError(t, err)

	now := time.Now().UTC()
	globalRef := profilepkg.GlobalInheritedRef()
	globalDefaults, err := profilepkg.NewInherited(globalRef, profilepkg.InheritedCreateInput{
		CreatedBy: "alice",
	}, now)
	require.NoError(t, err)
	require.NoError(t, globalDefaults.SetVariable("GLOBAL_ONLY", "global", "alice", now))
	require.NoError(t, globalDefaults.SetVariable("WORKSPACE_ONLY", "global", "alice", now))
	require.NoError(t, globalDefaults.SetVariable("SHARED", "global", "alice", now))
	require.NoError(t, profileStore.Create(context.Background(), globalDefaults))

	workspaceRef, err := profilepkg.WorkspaceInheritedRef("ops")
	require.NoError(t, err)
	workspaceDefaults, err := profilepkg.NewInherited(workspaceRef, profilepkg.InheritedCreateInput{
		CreatedBy: "alice",
	}, now)
	require.NoError(t, err)
	require.NoError(t, workspaceDefaults.SetVariable("WORKSPACE_ONLY", "workspace", "alice", now))
	require.NoError(t, workspaceDefaults.SetVariable("SHARED", "workspace", "alice", now))

	defaultToken, err := secretpkg.New(secretpkg.CreateInput{
		Ref:          workspaceRef.SecretRef("DEFAULT_TOKEN"),
		ProviderType: secretpkg.ProviderDaguManaged,
		CreatedBy:    "alice",
	}, now)
	require.NoError(t, err)
	require.NoError(t, secretStore.Create(context.Background(), defaultToken, &secretpkg.WriteValueInput{
		Value:     "workspace-default-secret",
		CreatedBy: "alice",
		CreatedAt: now,
	}))
	require.NoError(t, workspaceDefaults.SetSecret("DEFAULT_TOKEN", defaultToken.ID, "alice", now))
	require.NoError(t, profileStore.Create(context.Background(), workspaceDefaults))

	selected, err := profilepkg.New(profilepkg.CreateInput{
		Name:      "prod",
		CreatedBy: "alice",
	}, now)
	require.NoError(t, err)
	require.NoError(t, selected.SetVariable("SELECTED_ONLY", "selected", "alice", now))
	require.NoError(t, selected.SetVariable("SHARED", "selected", "alice", now))
	require.NoError(t, profileStore.Create(context.Background(), selected))

	dag := th.DAG(t, `
labels:
  - workspace=ops
steps:
  - name: step1
    run: echo "$GLOBAL_ONLY|$WORKSPACE_ONLY|$SELECTED_ONLY|$SHARED|$DEFAULT_TOKEN"
    output: RESPONSE`)

	dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{
		ProfileStore: profileStore,
		SecretStore:  secretStore,
		ProfileName:  "prod",
	}))
	dagAgent.RunSuccess(t)

	outputs := dag.ReadOutputs(t)
	require.Contains(t, outputs["response"], "global|workspace|selected|selected|*******")
	require.NotContains(t, outputs["response"], "workspace-default-secret")

	status := dagAgent.Status(th.Context)
	require.Equal(t, "prod", status.ProfileName)
	require.NotEmpty(t, status.ProfileResolvedAt)
	require.ElementsMatch(t, []ir.RuntimeProfileEntry{
		{Key: "GLOBAL_ONLY", Kind: "variable"},
		{Key: "WORKSPACE_ONLY", Kind: "variable"},
		{Key: "SHARED", Kind: "variable"},
		{Key: "DEFAULT_TOKEN", Kind: "secret"},
		{Key: "SELECTED_ONLY", Kind: "variable"},
	}, status.ProfileEntries)

	statusJSON, err := json.Marshal(status)
	require.NoError(t, err)
	require.NotContains(t, string(statusJSON), "workspace-default-secret")
	require.Contains(t, string(statusJSON), "*******")
}

func TestAgent_SubDAGRunVisibleWhileRunning(t *testing.T) {
	t.Parallel()

	th := test.Setup(t, test.WithBuiltExecutable())
	releaseFile := filepath.Join(t.TempDir(), "release-child")
	t.Cleanup(func() {
		_ = os.WriteFile(releaseFile, []byte("done"), 0600)
	})

	// Hold the child open until the test explicitly releases it so Windows can
	// observe persisted SubRuns without racing the child completion.
	th.CreateDAGFile(t, th.Config.Paths.DAGsDir, "child-slow", fmt.Appendf(nil, `
steps:
  - name: slow-step
    run: %q
`, waitForFileScript(releaseFile, 100*time.Millisecond)))

	// The preceding step must run long enough for the one-shot 100ms status timer
	// to fire (and exhaust itself) BEFORE run-child starts. This replicates the
	// production scenario where the bug manifests.
	parent := th.DAG(t, fmt.Sprintf(`
type: graph
steps:
  - name: pre-step
    run: %q
  - name: run-child
    action: dag.run
    with:
      dag: child-slow
    depends:
      - pre-step
`, test.Sleep(time.Second)))

	a := parent.Agent()
	runErr := make(chan error, 1)
	go func() {
		runErr <- a.Run(parent.Context)
	}()

	// SubRuns must be visible in persisted history before the child DAG completes.
	// RecentStatuses observes the same persisted status returned by the API.
	require.Eventually(t, func() bool {
		statuses, err := th.DAGRunRepository.RecentStatuses(th.Context, parent.Name, 1)
		if err != nil {
			return false
		}
		if len(statuses) == 0 || statuses[0].Status != ir.Running {
			return false
		}
		for _, node := range statuses[0].Nodes {
			if node.Step.Name == "run-child" && node.Status == ir.NodeRunning {
				return len(node.SubRuns) > 0
			}
		}
		return false
	}, subDAGVisibleTimeout(), 100*time.Millisecond,
		"SubRuns must be present in stored status while subDAG step is running")

	require.NoError(t, os.WriteFile(releaseFile, []byte("done"), 0600))
	require.NoError(t, <-runErr)
}

func TestAgent_LocalSubDAGRunDoesNotRequireDaguExecutable(t *testing.T) {
	t.Parallel()

	const parentRunID = "parent-run-in-process"

	th := test.Setup(t,
		test.WithConfigMutator(func(cfg *config.Config) {
			cfg.Paths.Executable = filepath.Join(t.TempDir(), "missing-dagu")
		}),
	)

	th.CreateDAGFile(t, th.Config.Paths.DAGsDir, "child-in-process", []byte(`
params:
  - TARGET: default
steps:
  - name: emit
    run: echo "child=${TARGET}"
    output: RESULT
`))

	parent := th.DAG(t, `
type: graph
steps:
  - name: run-child
    action: dag.run
    with:
      dag: child-in-process
      params:
        TARGET: from-parent
`)

	a := parent.Agent(test.WithDAGRunID(parentRunID))
	a.RunSuccess(t)

	status := a.Status(parent.Context)
	require.Len(t, status.Nodes, 1)
	require.Len(t, status.Nodes[0].SubRuns, 1)

	subRun := status.Nodes[0].SubRuns[0]
	attempt, err := th.DAGRunRepository.FindSubAttempt(
		th.Context,
		ir.NewDAGRunRef(parent.Name, parentRunID),
		subRun.DAGRunID,
	)
	require.NoError(t, err)

	childStatus, err := attempt.ReadStatus(th.Context)
	require.NoError(t, err)
	require.Equal(t, ir.Succeeded, childStatus.Status)
	require.Equal(t, []string{"TARGET=from-parent"}, childStatus.ParamsList)
	require.Len(t, childStatus.Nodes, 1)
	require.NotNil(t, childStatus.Nodes[0].OutputVariables)
	result, ok := childStatus.Nodes[0].OutputVariables.Load("RESULT")
	require.True(t, ok)
	require.Equal(t, "RESULT=child=from-parent", result)
}

func TestAgent_LocalSubDAGRunSetsArtifactDir(t *testing.T) {
	t.Parallel()

	const parentRunID = "parent-run-artifact"

	th := test.Setup(t,
		test.WithConfigMutator(func(cfg *config.Config) {
			cfg.Paths.Executable = filepath.Join(t.TempDir(), "missing-dagu")
		}),
	)

	th.CreateDAGFile(t, th.Config.Paths.DAGsDir, "child-artifact", []byte(`
steps:
  - name: write
    action: artifact.write
    with:
      path: reports/summary.txt
      content: child artifact
`))

	parent := th.DAG(t, `
type: graph
steps:
  - name: run-child
    action: dag.run
    with:
      dag: child-artifact
`)

	a := parent.Agent(test.WithDAGRunID(parentRunID))
	a.RunSuccess(t)

	status := a.Status(parent.Context)
	require.Len(t, status.Nodes, 1)
	require.Len(t, status.Nodes[0].SubRuns, 1)

	subRun := status.Nodes[0].SubRuns[0]
	attempt, err := th.DAGRunRepository.FindSubAttempt(
		th.Context,
		ir.NewDAGRunRef(parent.Name, parentRunID),
		subRun.DAGRunID,
	)
	require.NoError(t, err)

	childStatus, err := attempt.ReadStatus(th.Context)
	require.NoError(t, err)
	require.Equal(t, ir.Succeeded, childStatus.Status)
	require.NotEmpty(t, childStatus.ArchiveDir)

	data, err := os.ReadFile(filepath.Join(childStatus.ArchiveDir, "reports", "summary.txt"))
	require.NoError(t, err)
	require.Equal(t, "child artifact", string(data))
}

func TestAgent_DAGEnqueueQueuesChildWithoutWaiting(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	releaseFile := filepath.Join(t.TempDir(), "release-child")
	t.Cleanup(func() {
		_ = os.WriteFile(releaseFile, []byte("done"), 0600)
	})

	th.CreateDAGFile(t, th.Config.Paths.DAGsDir, "child-enqueued", fmt.Appendf(nil, `
params:
  - TARGET: default
  - OTHER: keep
steps:
  - name: wait
    run: %q
`, waitForFileScript(releaseFile, 100*time.Millisecond)))

	parent := th.DAG(t, `
type: graph
steps:
  - name: enqueue-child
    action: dag.enqueue
    with:
      dag: child-enqueued
      params:
        TARGET: async
      queue: background
`)

	a := parent.Agent()
	a.RunSuccess(t)

	status := a.Status(parent.Context)
	require.Len(t, status.Nodes, 1)
	require.Len(t, status.Nodes[0].SubRuns, 1)
	subRun := status.Nodes[0].SubRuns[0]
	require.Equal(t, "child-enqueued", subRun.DAGName)
	require.Contains(t, subRun.Params, `TARGET="async"`)

	items, err := th.QueueStore.List(th.Context, "background")
	require.NoError(t, err)
	require.Len(t, items, 1)

	ref, err := items[0].Data()
	require.NoError(t, err)
	require.Equal(t, ir.NewDAGRunRef("child-enqueued", subRun.DAGRunID), *ref)

	attempt, err := th.DAGRunRepository.FindAttempt(th.Context, *ref)
	require.NoError(t, err)
	childStatus, err := attempt.ReadStatus(th.Context)
	require.NoError(t, err)
	require.Equal(t, ir.Queued, childStatus.Status)
	require.Equal(t, ir.TriggerTypeSubDAG, childStatus.TriggerType)
	require.Equal(t, []string{"TARGET=async", "OTHER=keep"}, childStatus.ParamsList)
	require.Equal(t, *ref, childStatus.Root)
	require.True(t, childStatus.Parent.Zero())
}

func TestAgent_DAGEnqueueQueuedChildRunsFromQueue(t *testing.T) {
	t.Parallel()

	outputFile := filepath.Join(t.TempDir(), "child-output")
	th := test.Setup(t,
		test.WithBuiltExecutable(),
		test.WithConfigMutator(func(cfg *config.Config) {
			cfg.Queues.Enabled = true
			cfg.Queues.Config = append(cfg.Queues.Config, config.QueueConfig{
				Name:          "background",
				MaxActiveRuns: 1,
			})
		}),
	)

	profileStore := file.NewProfileStore(
		th.Context,
		th.Config,
		th.Backend.Collection(persis.CollectionProfiles),
	)
	prof, err := profilepkg.New(profilepkg.CreateInput{
		Name:      "prod",
		CreatedBy: "alice",
	}, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, profileStore.Create(th.Context, prof))

	th.CreateDAGFile(t, th.Config.Paths.DAGsDir, "child-queue-exec", fmt.Appendf(nil, `
params:
  - TARGET: default
steps:
  - name: write-output
    run: %q
`, writeEnvFileCommand(outputFile, ir.ParallelItemVariable)))

	parent := th.DAG(t, `
type: graph
steps:
  - name: enqueue-child
    action: dag.enqueue
    with:
      dag: child-queue-exec
      queue: background
      params:
        TARGET: async
    parallel:
      items:
        - preserved-item
`)

	a := parent.Agent(test.WithAgentOptions(agent.Options{
		ProfileStore: profileStore,
		ProfileName:  "prod",
	}))
	a.RunSuccess(t)

	status := a.Status(parent.Context)
	require.Len(t, status.Nodes, 1)
	require.Len(t, status.Nodes[0].SubRuns, 1)
	subRun := status.Nodes[0].SubRuns[0]
	require.Equal(t, "preserved-item", subRun.ParallelItem)
	ref := ir.NewDAGRunRef("child-queue-exec", subRun.DAGRunID)
	attempt, err := th.DAGRunRepository.FindAttempt(th.Context, ref)
	require.NoError(t, err)
	queuedStatus, err := attempt.ReadStatus(th.Context)
	require.NoError(t, err)
	require.Equal(t, "preserved-item", queuedStatus.ParallelItem)

	dagExecutor := scheduler.NewDAGExecutor(
		nil,
		launcher.NewSubCmdBuilder(th.Config),
		th.Config.DefaultExecMode,
		th.Config.Paths.BaseConfig,
		nil,
	)
	processor := scheduler.NewQueueProcessor(
		th.QueueStore,
		th.DAGRunRepository,
		th.ProcRepository,
		dagExecutor,
		th.Config.Queues,
		scheduler.WithBackoffConfig(scheduler.BackoffConfig{
			InitialInterval: 100 * time.Millisecond,
			MaxInterval:     250 * time.Millisecond,
			MaxRetries:      20,
		}),
	)
	processor.ProcessQueueItems(th.Context, "background")

	require.Eventually(t, func() bool {
		childStatus, err := th.DAGRunMgr.GetSavedStatus(th.Context, ref)
		return err == nil && childStatus.Status == ir.Succeeded
	}, subDAGVisibleTimeout(), 100*time.Millisecond)
	output, err := os.ReadFile(outputFile)
	require.NoError(t, err)
	require.Equal(t, "preserved-item", string(output))

	childStatus, err := th.DAGRunMgr.GetSavedStatus(th.Context, ref)
	require.NoError(t, err)
	require.Equal(t, "prod", childStatus.ProfileName)
	require.Equal(t, "preserved-item", childStatus.ParallelItem)

	require.Eventually(t, func() bool {
		processor.ProcessQueueItems(th.Context, "background")
		length, err := th.QueueStore.Len(th.Context, "background")
		return err == nil && length == 0
	}, subDAGVisibleTimeout(), 100*time.Millisecond)
}

func subDAGVisibleTimeout() time.Duration {
	if runtime.GOOS == "windows" {
		return 90 * time.Second
	}
	return 10 * time.Second
}

type failingWorkDirStore struct {
	dir           string
	snapshotErr   error
	snapshotCalls int
}

func (s *failingWorkDirStore) Materialize(context.Context, dagrun.WorkDirRef) (string, error) {
	return s.dir, nil
}

func (s *failingWorkDirStore) Snapshot(context.Context, dagrun.WorkDirRef, string) error {
	s.snapshotCalls++
	return s.snapshotErr
}

func (*failingWorkDirStore) Remove(context.Context, dagrun.WorkDirRef) error {
	return nil
}
