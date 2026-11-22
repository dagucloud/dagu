// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/stepmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withStepMocks(steps map[string]stepmock.Step) runnerOption {
	return func(cfg *runtime.Config) {
		cfg.ExecutorOverride = stepmock.New(steps)
	}
}

func readLog(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

// A mocked step writes the streams of its result in place of running its
// action. A step without a mock runs its own action.
func TestStepMockStartsNoProcess(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mockedMarker := filepath.Join(dir, "mocked")
	realMarker := filepath.Join(dir, "real")
	r := setupRunner(t, withStepMocks(map[string]stepmock.Step{
		"mocked": {Results: []stepmock.Result{{Stdout: "out\n", Stderr: "err\n"}}},
	}))
	result := r.newPlan(t,
		newStep("mocked", withCommand(createEmptyFileCommand(mockedMarker))),
		newStep("real", withCommand(createEmptyFileCommand(realMarker))),
	).assertRun(t, ir.Succeeded)

	assert.NoFileExists(t, mockedMarker)
	assert.FileExists(t, realMarker)
	mocked := result.nodeByName(t, "mocked")
	assert.Equal(t, "out\n", readLog(t, mocked.GetStdout()))
	assert.Equal(t, "err\n", readLog(t, mocked.GetStderr()))
}

// Each execution of a mocked step takes the next result, and after the last
// the last one repeats. The retry and done counts of the run status add up to
// the executions.
func TestStepMockResultsByExecution(t *testing.T) {
	t.Parallel()

	repeatLimit := func(limit int) stepOption {
		return func(step *ir.Step) { step.RepeatPolicy.Limit = limit }
	}
	for _, tc := range []struct {
		name         string
		opts         []stepOption
		results      []stepmock.Result
		wantRun      ir.Status
		wantStatus   ir.NodeStatus
		wantExitCode int
		wantStdout   string
		wantRuns     int
	}{
		{
			name: "Retries",
			opts: []stepOption{withRetryPolicy(3, 0)},
			results: []stepmock.Result{
				{ExitCode: 1, Stdout: "one\n"},
				{ExitCode: 2, Stdout: "two\n"},
				{Stdout: "three\n"},
				{Stdout: "four\n"},
			},
			wantRun:    ir.Succeeded,
			wantStatus: ir.NodeSucceeded,
			wantStdout: "one\ntwo\nthree\n",
			wantRuns:   3,
		},
		{
			name:         "LastRepeats",
			opts:         []stepOption{withRetryPolicy(2, 0)},
			results:      []stepmock.Result{{ExitCode: 3, Stdout: "fail\n"}},
			wantRun:      ir.Failed,
			wantStatus:   ir.NodeFailed,
			wantExitCode: 3,
			wantStdout:   "fail\nfail\nfail\n",
			wantRuns:     3,
		},
		{
			name:       "Repeats",
			opts:       []stepOption{withRepeatPolicy(true, 0), repeatLimit(3)},
			results:    []stepmock.Result{{Stdout: "a\n"}, {Stdout: "b\n"}},
			wantRun:    ir.Succeeded,
			wantStatus: ir.NodeSucceeded,
			wantStdout: "a\nb\nb\n",
			wantRuns:   3,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := setupRunner(t, withStepMocks(map[string]stepmock.Step{
				"build": {Results: tc.results},
			}))
			step := newStep("build", append(tc.opts, withCommand("exit 0"))...)
			result := r.newPlan(t, step).assertRun(t, tc.wantRun)

			node := result.nodeByName(t, "build")
			state := node.State()
			assert.Equal(t, tc.wantStatus, state.Status)
			assert.Equal(t, tc.wantExitCode, state.ExitCode)
			assert.Equal(t, tc.wantStdout, readLog(t, node.GetStdout()))
			assert.Equal(t, tc.wantRuns, state.RetryCount+state.DoneCount)
		})
	}
}

// A failed mocked step aborts its dependents and runs the failure handler.
// With continue_on failure, its dependents run.
func TestStepMockFailure(t *testing.T) {
	t.Parallel()

	failed := stepmock.Step{Results: []stepmock.Result{{ExitCode: 1, Stderr: "build failed\n"}}}

	t.Run("AbortsDependents", func(t *testing.T) {
		t.Parallel()

		marker := filepath.Join(t.TempDir(), "notified")
		r := setupRunner(t,
			withOnFailure(newHandlerStep(t, "onFailure", "", createEmptyFileCommand(marker))),
			withStepMocks(map[string]stepmock.Step{
				"build":     failed,
				"deploy":    {},
				"onFailure": {},
			}),
		)
		result := r.newPlan(t, successStep("build"), successStep("deploy", "build")).assertRun(t, ir.Failed)

		result.assertNodeStatus(t, "build", ir.NodeFailed)
		result.assertNodeStatus(t, "deploy", ir.NodeAborted)
		result.assertNodeStatus(t, "onFailure", ir.NodeSucceeded)
		assert.NoFileExists(t, marker)
	})

	t.Run("ContinueOnFailure", func(t *testing.T) {
		t.Parallel()

		r := setupRunner(t, withStepMocks(map[string]stepmock.Step{
			"build":  failed,
			"deploy": {},
		}))
		build := newStep("build", withCommand("exit 0"), withContinueOn(ir.ContinueOn{Failure: true}))
		result := r.newPlan(t, build, successStep("deploy", "build")).assertRun(t, ir.PartiallySucceeded)

		result.assertNodeStatus(t, "build", ir.NodeFailed)
		result.assertNodeStatus(t, "deploy", ir.NodeSucceeded)
	})
}

// The outputs of a successful mocked execution publish through the step's
// declared outputs (Spec 012), so a later step and a value-match
// precondition read them.
func TestStepMockPublishesDeclaredOutputs(t *testing.T) {
	t.Parallel()

	r := setupRunner(t, withStepMocks(map[string]stepmock.Step{
		"plan": {Results: []stepmock.Result{{Outputs: map[string]any{
			"risk":  "low",
			"notes": "first\nEOF\nlast",
			"meta":  map[string]any{"replicas": 2},
		}}}},
		"review": {},
		"merge":  {},
	}))
	plan := newStep("plan", withID("plan"), withCommand("exit 0"))
	plan.Outputs = []ir.StepOutputDeclaration{
		{Name: "risk", Type: ir.StepDeclaredOutputTypeString},
		{Name: "notes", Type: ir.StepDeclaredOutputTypeString},
		{Name: "meta", Type: ir.StepDeclaredOutputTypeJSON},
	}
	review := newStep("review", withDepends("plan"), withCommand("exit 0"),
		withPrecondition(&ir.Condition{Condition: "${steps.plan.outputs.risk}", Expected: "high"}))
	merge := newStep("merge", withDepends("plan"), withCommand("echo ${steps.plan.outputs.risk}"),
		withPrecondition(&ir.Condition{Condition: "${steps.plan.outputs.risk}", Expected: "low"}))
	result := r.newPlan(t, plan, review, merge).assertRun(t, ir.Succeeded)

	result.assertNodeStatus(t, "review", ir.NodeSkipped)
	result.assertNodeStatus(t, "merge", ir.NodeSucceeded)
	assert.Equal(t, []string{"low"}, result.nodeByName(t, "merge").Step().Commands[0].Args)
	outputs := result.nodeByName(t, "plan").State().StepOutputsValue
	require.NotNil(t, outputs)
	assert.JSONEq(t, `{"risk":"low","notes":"first\nEOF\nlast","meta":"{\"replicas\":2}"}`, *outputs)
}

// A failed mocked execution publishes no outputs. A later mocked step that
// reads one keeps the reference as text and still takes its result.
func TestStepMockFailedAttemptPublishesNoOutputs(t *testing.T) {
	t.Parallel()

	r := setupRunner(t, withStepMocks(map[string]stepmock.Step{
		"plan": {Results: []stepmock.Result{{ExitCode: 1, Outputs: map[string]any{"risk": "high"}}}},
		"use":  {Results: []stepmock.Result{{Stdout: "used\n"}}},
	}))
	plan := newStep("plan", withID("plan"), withCommand("exit 0"), withContinueOn(ir.ContinueOn{Failure: true}))
	plan.Outputs = []ir.StepOutputDeclaration{{Name: "risk", Type: ir.StepDeclaredOutputTypeString}}
	use := newStep("use", withDepends("plan"),
		withEnvVars("RISK=${steps.plan.outputs.risk}"),
		withCommand("echo ${steps.plan.outputs.risk}"))
	result := r.newPlan(t, plan, use).assertRun(t, ir.PartiallySucceeded)

	result.assertNodeStatus(t, "plan", ir.NodeFailed)
	assert.Nil(t, result.nodeByName(t, "plan").State().StepOutputsValue)
	result.assertNodeStatus(t, "use", ir.NodeSucceeded)
	usedNode := result.nodeByName(t, "use")
	assert.Equal(t, []string{"${steps.plan.outputs.risk}"}, usedNode.Step().Commands[0].Args)
	assert.Equal(t, "used\n", readLog(t, usedNode.GetStdout()))
}

// A mocked step keeps its executor type, so its command and script resolve to
// the same text as in a real run of the step. The real SSH step has no host,
// so it fails when its executor is built, after its fields resolve. Its
// command resolves Dagu values and leaves $HOME for the remote shell.
func TestStepMockResolvesFieldsByAction(t *testing.T) {
	t.Parallel()

	resolve := func(t *testing.T, step ir.Step, mocked bool, want ir.Status) ir.Step {
		t.Helper()

		var opts []runnerOption
		if mocked {
			opts = append(opts, withStepMocks(map[string]stepmock.Step{step.Name: {}}))
		}
		result := setupRunner(t, opts...).newPlan(t, step).assertRun(t, want)
		return result.nodeByName(t, step.Name).Step()
	}

	for _, tc := range []struct {
		step     ir.Step
		wantReal ir.Status
		// wantCommand is the resolved shell command, when checked.
		wantCommand string
	}{
		{
			step:     newStep("command", withEnvVars("TARGET=prod"), withCommand("echo ${TARGET} $HOME")),
			wantReal: ir.Succeeded,
		},
		{
			step:     newStep("script", withEnvVars("TARGET=prod"), withScript("echo ${TARGET} $HOME")),
			wantReal: ir.Succeeded,
		},
		{
			step: newStep("ssh", withExecutorType("ssh"), withEnvVars("TARGET=prod"),
				withCommand("echo ${TARGET} $HOME"), withScript("cd $HOME")),
			wantReal:    ir.Failed,
			wantCommand: "echo prod $HOME",
		},
	} {
		t.Run(tc.step.Name, func(t *testing.T) {
			t.Parallel()

			real := resolve(t, tc.step, false, tc.wantReal)
			mocked := resolve(t, tc.step, true, ir.Succeeded)
			assert.Equal(t, real.Commands, mocked.Commands)
			assert.Equal(t, real.Script, mocked.Script)
			if tc.wantCommand != "" {
				assert.Equal(t, tc.wantCommand, mocked.Commands[0].CmdWithArgs)
			}
		})
	}
}
