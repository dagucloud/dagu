// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	runtimeexec "github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/runtime/untrusted"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withUntrusted() stepOption {
	return func(step *ir.Step) {
		step.Untrusted = true
	}
}

// untrustedPlan plans steps with the untrusted block cfg. The DAG of the run
// context holds the steps, and workDir is both the work directory of the run
// and the working directory of its steps.
func untrustedPlan(t *testing.T, cfg config.UntrustedConfig, workDir string, steps ...ir.Step) planHelper {
	t.Helper()
	r := setupRunner(t)
	r.Config.Untrusted = cfg
	ph := r.newPlan(t, steps...)
	ph.workDir = workDir
	ph.steps = steps
	ph.contextOpts = []runtime.ContextOption{runtime.WithWorkDir(workDir)}
	return ph
}

func skipWithoutPosixShell(t *testing.T) {
	t.Helper()
	if windowsShellTest() {
		t.Skip("the step scripts need a POSIX shell")
	}
}

// errorExecutor returns err from Run and counts the runs. With wait set, Run
// returns only once its context ends.
type errorExecutor struct {
	err  error
	wait bool
	runs atomic.Int32
}

func (e *errorExecutor) SetStdout(io.Writer)  {}
func (e *errorExecutor) SetStderr(io.Writer)  {}
func (e *errorExecutor) Kill(os.Signal) error { return nil }
func (e *errorExecutor) Run(ctx context.Context) error {
	e.runs.Add(1)
	if e.wait {
		<-ctx.Done()
	}
	return e.err
}

// registerErrorExecutor registers action under a type of its own for the test.
func registerErrorExecutor(t *testing.T, action *errorExecutor) string {
	t.Helper()
	executorType := "test-untrusted-" + t.Name()
	runtimeexec.RegisterExecutor(executorType, func(context.Context, ir.Step) (runtimeexec.Executor, error) {
		return action, nil
	}, nil, registry.ExecutorCapabilities{})
	t.Cleanup(func() { runtimeexec.UnregisterExecutor(executorType) })
	return executorType
}

// Without a restricted user and with the isolation on, an untrusted step fails
// before its process starts (Spec 103, "Without isolation").
func TestUntrustedStepNeedsIsolation(t *testing.T) {
	t.Parallel()
	skipWithoutPosixShell(t)

	marker := filepath.Join(t.TempDir(), "ran")
	result := untrustedPlan(t, config.UntrustedConfig{}, t.TempDir(),
		newStep("verify", withUntrusted(), withCommand("touch "+marker))).assertRun(t, ir.Failed)

	result.assertNodeStatus(t, "verify", ir.NodeFailed)
	require.ErrorIs(t, result.nodeByName(t, "verify").Error(), untrusted.ErrIsolationRequired)
	assert.NoFileExists(t, marker)
}

// With untrusted.isolation off, an untrusted step runs as the process user
// without the DAGU_ variables but DAGU_OUTPUT_FILE, and its log says that the
// isolation is off. A trusted step of the run keeps its DAGU_ variables.
func TestUntrustedStepWithIsolationOff(t *testing.T) {
	t.Parallel()
	skipWithoutPosixShell(t)

	names := `env | grep '^DAGU_' | cut -d= -f1 | sort | tr '\n' ' '`
	result := untrustedPlan(t, config.UntrustedConfig{IsolationOff: true}, t.TempDir(),
		newStep("verify", withUntrusted(), withEnvVars("DAGU_TEST_VAR=x"), withScript(names), withOutput("NAMES")),
		newStep("check", withEnvVars("DAGU_TEST_VAR=x"), withScript(names), withOutput("NAMES")),
	).assertRun(t, ir.Succeeded)

	verify := result.nodeByName(t, "verify")
	assert.Equal(t, "DAGU_OUTPUT_FILE", verify.OutputVariablesMap()["NAMES"])
	assert.Contains(t, result.nodeByName(t, "check").OutputVariablesMap()["NAMES"], "DAGU_TEST_VAR")
	stderr, err := os.ReadFile(verify.NodeData().State.Stderr)
	require.NoError(t, err)
	assert.Contains(t, string(stderr), untrusted.IsolationOffLine)
}

// An untrusted step whose action does not start its processes through the
// rules of an untrusted step fails before the action runs.
func TestUntrustedStepNeedsUntrustedRunner(t *testing.T) {
	t.Parallel()

	action := &errorExecutor{}
	result := untrustedPlan(t, config.UntrustedConfig{IsolationOff: true}, t.TempDir(),
		newStep("verify", withUntrusted(), withExecutorType(registerErrorExecutor(t, action)))).assertRun(t, ir.Failed)

	result.assertNodeStatus(t, "verify", ir.NodeFailed)
	assert.ErrorContains(t, result.nodeByName(t, "verify").Error(), "cannot run as an untrusted step")
	assert.Zero(t, action.runs.Load())
}

// In a run with an untrusted step, git in a trusted process opens a bare
// repository only when the command names it (Spec 103, "Protections for later
// trusted steps").
func TestTrustedStepOfUntrustedRunGetsGitSafety(t *testing.T) {
	t.Parallel()
	skipWithoutPosixShell(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	result := untrustedPlan(t, config.UntrustedConfig{IsolationOff: true}, t.TempDir(),
		newStep("verify", withUntrusted(), withCommand("true")),
		newStep("check", withCommand("git config --get safe.bareRepository"), withOutput("SETTING")),
	).assertRun(t, ir.Succeeded)

	assert.Equal(t, "explicit", result.nodeByName(t, "check").OutputVariablesMap()["SETTING"])
}

// In a run with an untrusted step, a redirect into the work directory does not
// follow a link that an untrusted step could have made to a file of the worker.
func TestUntrustedRunRedirectRefusesLink(t *testing.T) {
	t.Parallel()
	if goruntime.GOOS != "linux" {
		t.Skip("the link checks need Linux")
	}

	work := t.TempDir()
	outside := filepath.Join(t.TempDir(), "worker-file")
	require.NoError(t, os.WriteFile(outside, []byte("kept"), 0o600))
	link := filepath.Join(work, "report.txt")
	require.NoError(t, os.Symlink(outside, link))

	result := untrustedPlan(t, config.UntrustedConfig{IsolationOff: true}, work,
		newStep("verify", withUntrusted(), withCommand("true")),
		newStep("report", withDepends("verify"), withCommand("echo report"), withStdout(link)),
	).assertRun(t, ir.Failed)

	result.assertNodeStatus(t, "report", ir.NodeFailed)
	assert.ErrorContains(t, result.nodeByName(t, "report").Error(), "symbolic link")
	data, err := os.ReadFile(outside)
	require.NoError(t, err)
	assert.Equal(t, "kept", string(data))
}

// A work directory that an untrusted step got and that no restore made safe,
// as after a stop of the process during the step, keeps each step of the run
// from running, also a trusted step and a DAG precondition, and stops the run.
// A marker that appears during the run stops the next step at its gate.
func TestWorkDirNotGivenBackStopsTheRun(t *testing.T) {
	t.Parallel()
	skipWithoutPosixShell(t)

	for name, atStart := range map[string]bool{"at the start": true, "during the run": false} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			work := t.TempDir()
			marker := work + ".untrusted-handoff"
			if atStart {
				require.NoError(t, os.WriteFile(marker, nil, 0o600))
			}
			ran := t.TempDir()
			ph := untrustedPlan(t, config.UntrustedConfig{IsolationOff: true}, work,
				newStep("mark", withCommand("touch "+marker)),
				newStep("check", withDepends("mark"), withCommand("touch "+filepath.Join(ran, "step"))),
				newStep("verify", withDepends("check"), withUntrusted(), withCommand("true")),
			)
			dag := &ir.DAG{
				Name: "test_dag", WorkingDir: work, Steps: ph.steps,
				Preconditions: []*ir.Condition{{Condition: "touch " + filepath.Join(ran, "precondition")}},
			}
			ctx := runtime.NewContext(ph.Context, dag, ph.cfg.DAGRunID, filepath.Join(ph.cfg.LogDir, "run.log"), ph.contextOpts...)

			err := ph.runner.Run(ctx, ph.Plan, nil)

			require.ErrorIs(t, err, untrusted.ErrRunAborted)
			assert.ErrorContains(t, err, "not given back safely")
			assert.Equal(t, ir.Aborted, ph.runner.Status(ctx, ph.Plan))
			if atStart {
				assert.NoFileExists(t, filepath.Join(ran, "precondition"))
			}
			assert.NoFileExists(t, filepath.Join(ran, "step"))
		})
	}
}

// A step error that marks the run as unsafe fails the step and stops the run:
// a later step does not start, even when the failed step lets the run
// continue. In the body of a foreach step, the error also keeps the next item
// from running its body, and it stops the run when the timeout of the foreach
// step ends the body.
func TestRunAbortedStopsTheRun(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		inForeach bool
		// timeout ends the foreach step while its body waits. It leaves the
		// body time to start its action.
		timeout time.Duration
	}{
		"step":            {},
		"foreach body":    {inForeach: true},
		"foreach timeout": {inForeach: true, timeout: platformTestDuration(time.Second, 3*time.Second)},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			action := &errorExecutor{err: fmt.Errorf("end: %w", untrusted.ErrRunAborted), wait: tc.timeout > 0}
			actionType := registerErrorExecutor(t, action)
			continueOn := withContinueOn(ir.ContinueOn{Failure: true})
			verify := newStep("verify", withExecutorType(actionType), continueOn)
			if tc.inForeach {
				verify = newStep("verify", withExecutorType(ir.ExecutorTypeForeach), continueOn, withStepTimeout(tc.timeout))
				verify.Foreach = &ir.ForeachConfig{
					Items:         []any{"a", "b"},
					MaxConcurrent: 1,
					Steps:         []ir.Step{newStep("agent", withExecutorType(actionType))},
				}
			}
			ph := untrustedPlan(t, config.UntrustedConfig{}, t.TempDir(),
				verify,
				newStep("next", withDepends("verify"), withCommand("true")),
			)
			dag := &ir.DAG{Name: "test_dag", WorkingDir: ph.workDir, Steps: ph.steps}
			ctx := runtime.NewContext(ph.Context, dag, ph.cfg.DAGRunID, filepath.Join(ph.cfg.LogDir, "run.log"), ph.contextOpts...)

			err := ph.runner.Run(ctx, ph.Plan, nil)

			require.ErrorIs(t, err, untrusted.ErrRunAborted)
			assert.Equal(t, int32(1), action.runs.Load())
			assert.Equal(t, ir.Aborted, ph.runner.Status(ctx, ph.Plan))
			assert.Equal(t, ir.NodeFailed, ph.GetNodeByName("verify").State().Status)
			assert.Equal(t, ir.NodeNotStarted, ph.GetNodeByName("next").State().Status)
		})
	}
}
