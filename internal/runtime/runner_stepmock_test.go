// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	_ "github.com/dagucloud/dagu/v2/internal/runtime/builtin/dag"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/runtime/stepmock"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withStepMocks(steps map[string]stepmock.Step) runnerOption {
	return withStepMockSet(stepmock.New(steps, nil))
}

// withFailureHandlerMock scripts the steps and the failure handler.
func withFailureHandlerMock(steps map[string]stepmock.Step, handler stepmock.Step) runnerOption {
	return withStepMockSet(stepmock.New(steps, map[ir.HandlerType]stepmock.Step{ir.HandlerOnFailure: handler}))
}

// withStepMockSet scripts the run with set, as a workflow test does.
func withStepMockSet(set *stepmock.Set) runnerOption {
	return func(cfg *runtime.Config) {
		cfg.ExecutorOverride = set
		cfg.ConditionOverride = set
	}
}

// executions returns how many times the step's action ran.
func executions(t *testing.T, plan *runtime.Plan, name string) int {
	t.Helper()

	node := plan.GetNodeByName(name)
	require.NotNil(t, node)
	state := node.State()
	return state.RetryCount + state.DoneCount
}

func readLog(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

// loadSteps builds the steps of a DAG from YAML, as a run of the DAG does.
func loadSteps(t *testing.T, data string) []ir.Step {
	t.Helper()

	dag, err := spec.LoadYAML(t.Context(), []byte(data))
	require.NoError(t, err)
	return dag.Steps
}

// outputSource is a plan step that publishes named outputs through one source
// that Spec 007 lists, with the other channels a real run of it publishes to.
// A real run would not publish the mocked values from any source: the mocks
// write stdout that is not JSON and create no file, and the action publishes
// values of its own.
type outputSource struct {
	plan string
	// stepOutput reports that the source publishes ${<step>.output}.
	stepOutput bool
	// runOutputs reports that the source publishes to the outputs the run
	// collects.
	runOutputs bool
}

var planSources = map[string]outputSource{
	"OutputFile": {plan: `
  - id: plan
    run: ./plan.sh
    outputs:
      - name: risk
      - name: replicas
        type: json`},
	"OutputSchema": {plan: `
  - id: plan
    run: ./plan.sh
    output_schema:
      type: object
      required: [risk, replicas]
      properties:
        risk: {type: string}
        replicas: {type: integer}`, stepOutput: true},
	"ObjectFormOutput": {plan: `
  - id: plan
    run: ./plan.sh
    output:
      risk: {from: stdout, decode: json, select: .risk}
      replicas: {from: file, path: plan.json, decode: json}`, stepOutput: true, runOutputs: true},
	"StdoutOutputs": {plan: `
  - id: plan
    run: ./plan.sh
    stdout:
      outputs:
        fields:
          risk: {decode: json, select: .risk}
          replicas: {decode: json, select: .replicas}`, runOutputs: true},
	"Action": {plan: `
  - id: plan
    action: outputs.write
    with:
      values:
        risk: high
        replicas: 1`, runOutputs: true},
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
		name           string
		opts           []stepOption
		results        []stepmock.Result
		wantRun        ir.Status
		wantStatus     ir.NodeStatus
		wantExitCode   int
		wantStdout     string
		wantExecutions int
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
			wantRun:        ir.Succeeded,
			wantStatus:     ir.NodeSucceeded,
			wantStdout:     "one\ntwo\nthree\n",
			wantExecutions: 3,
		},
		{
			name:           "LastRepeats",
			opts:           []stepOption{withRetryPolicy(2, 0)},
			results:        []stepmock.Result{{ExitCode: 3, Stdout: "fail\n"}},
			wantRun:        ir.Failed,
			wantStatus:     ir.NodeFailed,
			wantExitCode:   3,
			wantStdout:     "fail\nfail\nfail\n",
			wantExecutions: 3,
		},
		{
			name:           "Repeats",
			opts:           []stepOption{withRepeatPolicy(true, 0), repeatLimit(3)},
			results:        []stepmock.Result{{Stdout: "a\n"}, {Stdout: "b\n"}},
			wantRun:        ir.Succeeded,
			wantStatus:     ir.NodeSucceeded,
			wantStdout:     "a\nb\nb\n",
			wantExecutions: 3,
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
			assert.Equal(t, tc.wantExecutions, state.RetryCount+state.DoneCount)
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
			withFailureHandlerMock(map[string]stepmock.Step{
				"build":  failed,
				"deploy": {},
			}, stepmock.Step{}),
		)
		result := r.newPlan(t, successStep("build"), successStep("deploy", "build")).assertRun(t, ir.Failed)

		result.assertNodeStatus(t, "build", ir.NodeFailed)
		result.assertNodeStatus(t, "deploy", ir.NodeAborted)
		result.assertNodeStatus(t, "onFailure", ir.NodeSucceeded)
		assert.NoFileExists(t, marker)
	})

	// A step named like the failure handler takes only its own mock, and
	// the unmocked handler runs its own action.
	t.Run("StepNamedLikeHandler", func(t *testing.T) {
		t.Parallel()

		marker := filepath.Join(t.TempDir(), "notified")
		r := setupRunner(t,
			withOnFailure(newHandlerStep(t, "onFailure", "", createEmptyFileCommand(marker))),
			withStepMocks(map[string]stepmock.Step{"onFailure": failed}),
		)
		r.newPlan(t, successStep("onFailure")).assertRun(t, ir.Failed)

		assert.Equal(t, ir.NodeSucceeded, r.runner.HandlerNode(ir.HandlerOnFailure).State().Status)
		assert.FileExists(t, marker)
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
// precondition read them. A json output publishes a string as a JSON string.
func TestStepMockPublishesDeclaredOutputs(t *testing.T) {
	t.Parallel()

	r := setupRunner(t, withStepMocks(map[string]stepmock.Step{
		"plan": {Results: []stepmock.Result{{Outputs: map[string]any{
			"risk":  "low",
			"notes": "first\nlast",
			"meta":  map[string]any{"replicas": 2},
			"color": "blue",
		}}}},
		"review": {},
		"merge":  {},
	}))
	plan := newStep("plan", withID("plan"), withCommand("exit 0"))
	plan.Outputs = []ir.StepOutputDeclaration{
		{Name: "risk", Type: ir.StepDeclaredOutputTypeString},
		{Name: "notes", Type: ir.StepDeclaredOutputTypeString},
		{Name: "meta", Type: ir.StepDeclaredOutputTypeJSON},
		{Name: "color", Type: ir.StepDeclaredOutputTypeJSON},
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
	assert.JSONEq(t, `{"risk":"low","notes":"first\nlast","meta":"{\"replicas\":2}","color":"\"blue\""}`, *outputs)
}

// A successful mocked execution publishes its outputs whatever the source of
// the step's named outputs. A later step resolves them, and they reach
// ${<step>.output} and the outputs the run collects where a real run of the
// source does. A value that is not a string publishes as its JSON text.
func TestStepMockPublishesNamedOutputs(t *testing.T) {
	t.Parallel()

	for name, source := range planSources {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := setupRunner(t, withStepMocks(map[string]stepmock.Step{
				"plan": {Results: []stepmock.Result{{
					Stdout:  "not json\n",
					Outputs: map[string]any{"risk": "low", "replicas": 2},
				}}},
				"deploy": {},
			}))
			steps := loadSteps(t, "steps:"+source.plan+`
  - id: deploy
    run: >-
      ./deploy.sh ${steps.plan.outputs.risk} ${steps.plan.outputs.replicas}
      ${plan.outputs.risk} ${plan.output.risk}
`)
			result := r.newPlan(t, steps...).assertRun(t, ir.Succeeded)

			stepOutput := "${plan.output.risk}"
			if source.stepOutput {
				stepOutput = "low"
			}
			assert.Equal(t, []string{"low", "2", "low", stepOutput}, result.nodeByName(t, "deploy").Step().Commands[0].Args)
			var runOutputs map[string]string
			if source.runOutputs {
				runOutputs = map[string]string{"risk": "low", "replicas": "2"}
			}
			assert.Equal(t, runOutputs, result.nodeByName(t, "plan").NodeData().OutputsValueStringMap())
		})
	}
}

// A failed mocked execution publishes no outputs, whatever their source. A
// later mocked step that reads one keeps the reference as text and still
// takes its result.
func TestStepMockFailedAttemptPublishesNoOutputs(t *testing.T) {
	t.Parallel()

	for name, source := range planSources {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := setupRunner(t, withStepMocks(map[string]stepmock.Step{
				"plan": {Results: []stepmock.Result{{ExitCode: 1, Outputs: map[string]any{"risk": "high", "replicas": 3}}}},
				"use":  {Results: []stepmock.Result{{Stdout: "used\n"}}},
			}))
			steps := loadSteps(t, "steps:"+source.plan+`
    continue_on: failed
  - id: use
    env:
      - RISK: ${steps.plan.outputs.risk}
    run: echo ${steps.plan.outputs.risk} ${plan.output.risk}
`)
			result := r.newPlan(t, steps...).assertRun(t, ir.PartiallySucceeded)

			result.assertNodeStatus(t, "plan", ir.NodeFailed)
			planNode := result.nodeByName(t, "plan")
			assert.Nil(t, planNode.State().StepOutputsValue)
			assert.Nil(t, planNode.NodeData().OutputsValueStringMap())
			result.assertNodeStatus(t, "use", ir.NodeSucceeded)
			usedNode := result.nodeByName(t, "use")
			assert.Equal(t, []string{"${steps.plan.outputs.risk}", "${plan.output.risk}"}, usedNode.Step().Commands[0].Args)
			assert.Equal(t, "used\n", readLog(t, usedNode.GetStdout()))
		})
	}
}

// String-form output captures the stdout of a mocked step as in a real run,
// beside a source of named outputs that would decode it.
func TestStepMockCapturesOutputVariable(t *testing.T) {
	t.Parallel()

	r := setupRunner(t, withStepMocks(map[string]stepmock.Step{
		"plan":   {Results: []stepmock.Result{{Stdout: "not json\n", Outputs: map[string]any{"risk": "low"}}}},
		"deploy": {},
	}))
	steps := loadSteps(t, `
steps:
  - id: plan
    run: ./plan.sh
    output: PLAN
    stdout:
      outputs:
        field: risk
        decode: json
  - id: deploy
    run: ./deploy.sh "${PLAN}" ${steps.plan.outputs.risk}
`)
	result := r.newPlan(t, steps...).assertRun(t, ir.Succeeded)

	assert.Equal(t, []string{"not json", "low"}, result.nodeByName(t, "deploy").Step().Commands[0].Args)
}

// A real run reads the step's stdin and the file an object-form output names,
// so it fails when they are missing. A mocked step reads neither and
// publishes its outputs.
func TestStepMockReadsNoFiles(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "absent.txt")
	for _, tc := range []struct {
		name    string
		plan    string
		outputs map[string]any
		// wantOutputs is the published outputs payload, when any.
		wantOutputs string
	}{
		{
			name: "Stdin",
			plan: fmt.Sprintf(`
  - id: plan
    run: exit 0
    stdin: %q`, missing),
		},
		{
			name: "ObjectFormFile",
			plan: fmt.Sprintf(`
  - id: plan
    run: exit 0
    output:
      report: {from: file, path: %q}`, missing),
			outputs:     map[string]any{"report": "ok"},
			wantOutputs: `{"report":"ok"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			yaml := "steps:" + tc.plan
			unmocked := setupRunner(t).newPlan(t, loadSteps(t, yaml)...).assertRun(t, ir.Failed)
			assert.ErrorContains(t, unmocked.nodeByName(t, "plan").Error(), "absent.txt")

			r := setupRunner(t, withStepMocks(map[string]stepmock.Step{
				"plan": {Results: []stepmock.Result{{Outputs: tc.outputs}}},
			}))
			result := r.newPlan(t, loadSteps(t, yaml)...).assertRun(t, ir.Succeeded)

			outputs := result.nodeByName(t, "plan").State().StepOutputsValue
			if tc.wantOutputs == "" {
				assert.Nil(t, outputs)
				return
			}
			require.NotNil(t, outputs)
			assert.JSONEq(t, tc.wantOutputs, *outputs)
		})
	}
}

// An unmocked router routes on a mocked output, and the targets it does not
// match end skipped.
func TestStepMockRoutesOnMockedOutput(t *testing.T) {
	t.Parallel()

	r := setupRunner(t, withStepMocks(map[string]stepmock.Step{
		"plan":   {Results: []stepmock.Result{{Outputs: map[string]any{"risk": "low"}}}},
		"merge":  {},
		"review": {},
	}))
	steps := loadSteps(t, `
type: graph
steps:
  - id: plan
    run: ./plan.sh
    stdout:
      outputs:
        field: risk
  - id: route
    action: router.route
    with:
      value: ${steps.plan.outputs.risk}
      routes:
        low: [merge]
        high: [review]
  - id: merge
    run: ./merge.sh
  - id: review
    run: ./review.sh
`)
	result := r.newPlan(t, steps...).assertRun(t, ir.Succeeded)

	result.assertNodeStatus(t, "route", ir.NodeSucceeded)
	result.assertNodeStatus(t, "merge", ir.NodeSucceeded)
	result.assertNodeStatus(t, "review", ir.NodeSkipped)
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

			unmocked := resolve(t, tc.step, false, tc.wantReal)
			mocked := resolve(t, tc.step, true, ir.Succeeded)
			assert.Equal(t, unmocked.Commands, mocked.Commands)
			assert.Equal(t, unmocked.Script, mocked.Script)
			if tc.wantCommand != "" {
				assert.Equal(t, tc.wantCommand, mocked.Commands[0].CmdWithArgs)
			}
		})
	}
}

// childRuns records the child runs that steps start or queue. Each run it
// starts publishes outputs and succeeds, or fails with err when err is set.
type childRuns struct {
	mu      sync.Mutex
	runIDs  []string
	outputs map[string]any
	err     error
}

var (
	_ executor.SubWorkflowRunner = (*childRuns)(nil)
	_ executor.Enqueuer          = (*childRuns)(nil)
)

func (c *childRuns) started() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.runIDs)
}

func (c *childRuns) record(runID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.runIDs = append(c.runIDs, runID)
}

func (*childRuns) ShouldRun(context.Context, executor.SubWorkflowRequest) bool { return true }

func (c *childRuns) Run(_ context.Context, req executor.SubWorkflowRequest) (*ir.RunStatus, error) {
	c.record(req.RunID)
	status := ir.Succeeded
	if c.err != nil {
		status = ir.Failed
	}
	return &ir.RunStatus{Name: req.DAG.Name, DAGRunID: req.RunID, Status: status, OutputValues: c.outputs}, c.err
}

func (c *childRuns) Retry(ctx context.Context, req executor.SubWorkflowRetryRequest) (*ir.RunStatus, error) {
	return c.Run(ctx, req.SubWorkflowRequest)
}

func (*childRuns) Cancel(context.Context, executor.SubWorkflowCancelRequest) error { return nil }

func (c *childRuns) Enqueue(_ context.Context, req executor.EnqueueRequest) (executor.EnqueueResult, error) {
	c.record(req.RunID)
	return executor.EnqueueResult{Status: ir.Queued}, nil
}

// webNodes is an inventory in which every selector matches web01 and web02.
type webNodes struct{}

func (webNodes) Select(context.Context, string) ([]inventory.ResolvedNode, error) {
	return []inventory.ResolvedNode{{Name: "web01"}, {Name: "web02"}}, nil
}

func (n webNodes) Named(ctx context.Context, names []string) ([]inventory.ResolvedNode, error) {
	nodes, err := n.Select(ctx, "")
	return inventory.Named(nodes, names), err
}

// deployOneDAG returns a child DAG that runs on a node, for a dag.run step
// with nodes.
func deployOneDAG() *ir.DAG {
	return &ir.DAG{
		Name:         "deploy-one",
		NodeRequired: true,
		YamlData:     []byte("name: deploy-one\nnode: required\nsteps:\n  - run: ./deploy.sh\n"),
	}
}

func withNodes(selector string) stepOption {
	return func(step *ir.Step) {
		step.Nodes = &ir.NodeTargets{Selector: selector, MaxConcurrent: 1}
	}
}

func withSubDAG(name string) stepOption {
	return func(step *ir.Step) {
		step.SubDAG = &ir.SubDAG{Name: name, Params: "ENV=prod"}
	}
}

// A mock on a step that runs child DAGs replaces every child: the step takes
// the mock's result, and no child run starts, is queued, or is recorded on
// the step. Results count executions of the step, not children, so the
// second result is never taken. Unmocked, the same step starts its children
// in the same run context.
func TestStepMockReplacesChildRuns(t *testing.T) {
	t.Parallel()

	localDAGs := map[string]*ir.DAG{
		"deploy":     {Name: "deploy", YamlData: []byte("name: deploy\nsteps:\n  - run: ./deploy.sh\n")},
		"deploy-one": deployOneDAG(),
	}
	parallel := func(step *ir.Step) {
		step.Parallel = &ir.ParallelConfig{Items: []ir.ParallelItem{{Value: "a"}, {Value: "b"}}}
	}

	for _, tc := range []struct {
		name string
		step ir.Step
	}{
		{name: "DAGRun", step: newStep("rollout", withExecutorType(ir.ExecutorTypeDAG), withSubDAG("deploy"))},
		{name: "DAGEnqueue", step: newStep("rollout", withExecutorType(ir.ExecutorTypeDAGEnqueue), withSubDAG("deploy"))},
		{name: "Parallel", step: newStep("rollout", withExecutorType(ir.ExecutorTypeParallel), withSubDAG("deploy"), parallel)},
		{name: "NodesDAGRun", step: newStep("rollout", withExecutorType(ir.ExecutorTypeDAG), withSubDAG("deploy-one"), withNodes("web"))},
		{name: "NodesCommand", step: newStep("rollout", withCommand("./deploy.sh"), withNodes("web"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			run := func(t *testing.T, opts ...runnerOption) (*runtime.Node, []string) {
				t.Helper()

				runs := &childRuns{}
				r := setupRunner(t, opts...)
				r.Context = executor.WithSubWorkflowRunner(r.Context, runs)
				ph := r.newPlan(t, tc.step)
				ph.steps = []ir.Step{tc.step}
				ph.localDAGs = localDAGs
				ph.contextOpts = []runtime.ContextOption{
					runtime.WithRootDAGRun(runtime.NewDAGRunRef("test_dag", r.cfg.DAGRunID)),
					runtime.WithNodeSelector(webNodes{}),
				}
				return ph.assertRun(t, ir.Succeeded).nodeByName(t, tc.step.Name), runs.started()
			}

			unmocked, started := run(t)
			assert.NotEmpty(t, started)
			assert.NotEmpty(t, unmocked.State().SubRuns)

			mocked, started := run(t, withStepMocks(map[string]stepmock.Step{
				tc.step.Name: {Results: []stepmock.Result{{Stdout: "first\n"}, {Stdout: "second\n"}}},
			}))
			assert.Empty(t, started)
			assert.Empty(t, mocked.State().SubRuns)
			assert.Equal(t, "first\n", readLog(t, mocked.GetStdout()))
		})
	}
}

// A mocked step with nodes matches no nodes, so it takes its results in a run
// without an inventory, where the real step would fail before any node. A
// command step with nodes retries each node's child run (Spec 089), so its
// mock takes one result per step start. A dag.run step with nodes retries as
// a step and takes the next result.
func TestStepMockNodes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name           string
		action         []stepOption
		wantRun        ir.Status
		wantExecutions int
	}{
		{
			name:           "Command",
			action:         []stepOption{withCommand("./deploy.sh")},
			wantRun:        ir.Failed,
			wantExecutions: 1,
		},
		{
			name:           "DAGRun",
			action:         []stepOption{withExecutorType(ir.ExecutorTypeDAG), withSubDAG("deploy-one")},
			wantRun:        ir.Succeeded,
			wantExecutions: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := setupRunner(t, withStepMocks(map[string]stepmock.Step{
				"rollout": {Results: []stepmock.Result{{ExitCode: 1}, {}}},
			}))
			step := newStep("rollout", append(tc.action, withNodes("web"), withRetryPolicy(1, 0))...)
			state := r.newPlan(t, step).assertRun(t, tc.wantRun).nodeByName(t, "rollout").State()
			assert.Equal(t, tc.wantExecutions, state.RetryCount+state.DoneCount)
		})
	}
}

// A mocked step that runs child DAGs publishes any output names it is given,
// and a later step resolves them. A real parallel step or step with nodes
// publishes its children's outputs as a list, so the mock's outputs stay out
// of the outputs the run collects.
func TestStepMockChildRunOutputs(t *testing.T) {
	t.Parallel()

	parallel := func(step *ir.Step) {
		step.Parallel = &ir.ParallelConfig{Items: []ir.ParallelItem{{Value: "a"}, {Value: "b"}}}
	}
	for _, tc := range []struct {
		name           string
		action         []stepOption
		wantRunOutputs bool
	}{
		{
			name:           "DAGRun",
			action:         []stepOption{withExecutorType(ir.ExecutorTypeDAG), withSubDAG("deploy")},
			wantRunOutputs: true,
		},
		{
			name:   "Parallel",
			action: []stepOption{withExecutorType(ir.ExecutorTypeParallel), withSubDAG("deploy"), parallel},
		},
		{
			name:   "Nodes",
			action: []stepOption{withExecutorType(ir.ExecutorTypeDAG), withSubDAG("deploy-one"), withNodes("web")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := setupRunner(t, withStepMocks(map[string]stepmock.Step{
				"rollout": {Results: []stepmock.Result{{Outputs: map[string]any{"version": "v1.2.3", "replicas": 2}}}},
				"verify":  {},
			}))
			rollout := newStep("rollout", append(tc.action, withID("rollout"))...)
			verify := newStep("verify", withDepends("rollout"),
				withCommand("./verify.sh ${steps.rollout.outputs.version} ${steps.rollout.outputs.replicas}"))
			result := r.newPlan(t, rollout, verify).assertRun(t, ir.Succeeded)

			assert.Equal(t, []string{"v1.2.3", "2"}, result.nodeByName(t, "verify").Step().Commands[0].Args)
			var wantRunOutputs map[string]string
			if tc.wantRunOutputs {
				wantRunOutputs = map[string]string{"version": "v1.2.3", "replicas": "2"}
			}
			assert.Equal(t, wantRunOutputs, result.nodeByName(t, "rollout").NodeData().OutputsValueStringMap())
		})
	}
}

// A mocked step adds its outputs to the outputs the run collects, which feed
// outputs.json and a parent's outputValues, exactly as a real run of the step
// adds the outputs its action publishes. output_schema does not keep a
// dag.run step's outputs out, and a dag.enqueue step publishes none. A
// command step publishes its output_schema and output file outputs to
// references only, so the run collects neither. Whether an action publishes
// can depend on its operation, as for git and mail.
func TestStepMockCollectsOutputsAsRealRun(t *testing.T) {
	t.Parallel()

	localDAGs := map[string]*ir.DAG{
		"deploy": {Name: "deploy", YamlData: []byte("name: deploy\nsteps:\n  - run: ./deploy.sh\n")},
	}
	outputs := map[string]any{"version": "v1.2.3"}
	// collected runs step and returns the outputs the run collects from it. A
	// child run the step starts publishes outputs.
	collected := func(t *testing.T, step ir.Step, opts ...runnerOption) map[string]string {
		t.Helper()

		r := setupRunner(t, opts...)
		r.Context = executor.WithSubWorkflowRunner(r.Context, &childRuns{outputs: outputs})
		ph := r.newPlan(t, step)
		ph.steps = []ir.Step{step}
		ph.localDAGs = localDAGs
		ph.contextOpts = []runtime.ContextOption{
			runtime.WithRootDAGRun(runtime.NewDAGRunRef("test_dag", r.cfg.DAGRunID)),
		}
		return ph.assertRun(t, ir.Succeeded).nodeByName(t, step.Name).NodeData().OutputsValueStringMap()
	}
	mocked := func(t *testing.T, step ir.Step, outputs map[string]any) map[string]string {
		t.Helper()

		return collected(t, step, withStepMocks(map[string]stepmock.Step{
			step.Name: {Results: []stepmock.Result{{Outputs: outputs}}},
		}))
	}
	outputSchema := func(step *ir.Step) {
		step.OutputSchema = map[string]any{"type": "object"}
	}
	outputFile := func(step *ir.Step) {
		step.Outputs = []ir.StepOutputDeclaration{{Name: "version", Type: ir.StepDeclaredOutputTypeString}}
	}
	for _, tc := range []struct {
		name   string
		action []stepOption
		want   map[string]string
	}{
		{
			name:   "DAGRun",
			action: []stepOption{withExecutorType(ir.ExecutorTypeDAG), withSubDAG("deploy")},
			want:   map[string]string{"version": "v1.2.3"},
		},
		{
			name:   "DAGRunOutputSchema",
			action: []stepOption{withExecutorType(ir.ExecutorTypeDAG), withSubDAG("deploy"), outputSchema},
			want:   map[string]string{"version": "v1.2.3"},
		},
		{
			name:   "DAGEnqueue",
			action: []stepOption{withExecutorType(ir.ExecutorTypeDAGEnqueue), withSubDAG("deploy")},
		},
		{
			name:   "CommandOutputSchema",
			action: []stepOption{withCommand(`echo '{"version":"v1.2.3"}'`), outputSchema},
		},
		{
			name: "CommandOutputFile",
			action: []stepOption{withCommand(test.ForOS(
				`printf 'version=v1.2.3\n' >> "$DAGU_OUTPUT_FILE"`,
				`Add-Content -LiteralPath $env:DAGU_OUTPUT_FILE -Value 'version=v1.2.3'`,
			)), outputFile},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			step := newStep("rollout", append(tc.action, withID("rollout"))...)
			assert.Equal(t, tc.want, collected(t, step))
			assert.Equal(t, tc.want, mocked(t, step, outputs))
		})
	}

	// The mock takes the values the real run published, so the two runs
	// differ only in whether the run collects them.
	t.Run("GitWorktreeAdd", func(t *testing.T) {
		t.Parallel()

		step := loadSteps(t, fmt.Sprintf(`
steps:
  - id: rollout
    working_dir: '%s'
    action: git.worktree.add
`, filepath.ToSlash(initGitRepo(t))))[0]
		real := collected(t, step)
		require.Contains(t, real, "path")
		published := make(map[string]any, len(real))
		for name, value := range real {
			published[name] = value
		}
		assert.Equal(t, real, mocked(t, step, published))
	})

	// A real mail step needs a mail server, so only the mocked side runs.
	// mail.search and mail.organize publish their outputs, and mail.send
	// publishes none.
	for _, tc := range []struct {
		name string
		step string
		want map[string]string
	}{
		{
			name: "MailSearch",
			step: "action: mail.search\n    with: {mailbox: ops@example.com}",
			want: map[string]string{"version": "v1.2.3"},
		},
		{
			name: "MailOrganize",
			step: `action: mail.organize
    with: {mailbox: ops@example.com, emails: ["1"], mark: read}`,
			want: map[string]string{"version": "v1.2.3"},
		},
		{
			name: "MailSend",
			step: "action: mail.send\n    with: {to: a@example.com, subject: s, message: b}",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			step := loadSteps(t, "steps:\n  - id: rollout\n    "+tc.step+"\n")[0]
			assert.Equal(t, tc.want, mocked(t, step, outputs))
		})
	}
}

// initGitRepo creates a repository with one commit on main, apart from the
// host's Git configuration, and returns its path.
func initGitRepo(t *testing.T) string {
	t.Helper()

	repo := filepath.Join(t.TempDir(), "repo")
	require.NoError(t, os.MkdirAll(repo, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "base.txt"), []byte("base\n"), 0o600))
	for _, args := range [][]string{
		{"init"},
		{"symbolic-ref", "HEAD", "refs/heads/main"},
		{"add", "--", "base.txt"},
		{"commit", "-m", "initial"},
	} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL="+os.DevNull,
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=Dagu Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Dagu Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v\n%s", args, output)
	}
	return repo
}

// A mock on a foreach step replaces the whole construct: no item body runs,
// string-form output captures the mock's stdout in place of the aggregate,
// and the exit code alone decides between succeeded and failed. Results
// count executions of the step, not items.
func TestStepMockReplacesForeach(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		results    []stepmock.Result
		wantRun    ir.Status
		wantStatus ir.NodeStatus
		wantOutput string
	}{
		{
			name:       "Succeeded",
			results:    []stepmock.Result{{ExitCode: 1, Stdout: "retry"}, {Stdout: "mocked"}},
			wantRun:    ir.Succeeded,
			wantStatus: ir.NodeSucceeded,
			wantOutput: "mocked",
		},
		{
			name:       "Failed",
			results:    []stepmock.Result{{ExitCode: 1, Stdout: "failed"}},
			wantRun:    ir.Failed,
			wantStatus: ir.NodeFailed,
			wantOutput: "failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			marker := filepath.Join(t.TempDir(), "body")
			r := setupRunner(t, withStepMocks(map[string]stepmock.Step{"each": {Results: tc.results}}))
			each := newStep("each", withExecutorType(ir.ExecutorTypeForeach), withOutput("RESULT"), withRetryPolicy(1, 0))
			each.Foreach = &ir.ForeachConfig{
				Items: []any{"one", "two", "three"},
				Steps: []ir.Step{newStep("body", withCommand(createEmptyFileCommand(marker)))},
			}
			result := r.newPlan(t, each).assertRun(t, tc.wantRun)

			assert.NoFileExists(t, marker)
			node := result.nodeByName(t, "each")
			state := node.State()
			assert.Equal(t, tc.wantStatus, state.Status)
			assert.Equal(t, 2, state.RetryCount+state.DoneCount)
			output, ok := node.NodeData().StringFormOutputValue()
			require.True(t, ok)
			assert.Equal(t, tc.wantOutput, output)
		})
	}
}

// A scripted precondition decides each condition that runs a command, which
// then does not run, and leaves the other conditions to evaluate. Each case
// gates a step whose own action creates a marker, and records whether a
// command of a condition ran.
func TestStepMockPrecondition(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		mock stepmock.Step
		// conditions builds the step's preconditions. A command of one that
		// runs creates ranPath.
		conditions func(ranPath string) []*ir.Condition
		wantStatus ir.NodeStatus
		wantAction bool
		wantRan    bool
	}{
		{
			name: "NotMetSkips",
			mock: stepmock.Step{Precondition: new(false)},
			conditions: func(ran string) []*ir.Condition {
				return []*ir.Condition{{Condition: createEmptyFileCommand(ran)}}
			},
			wantStatus: ir.NodeSkipped,
		},
		{
			name: "MetPassesNegated",
			mock: stepmock.Step{Precondition: new(true)},
			conditions: func(ran string) []*ir.Condition {
				return []*ir.Condition{{Condition: createEmptyFileCommand(ran), Negate: true}}
			},
			wantStatus: ir.NodeSucceeded,
		},
		{
			name: "SubstitutingEval",
			mock: stepmock.Step{Precondition: new(false)},
			conditions: func(ran string) []*ir.Condition {
				return []*ir.Condition{{Eval: "$(" + createEmptyFileCommand(ran) + ")ready", Expected: "ready"}}
			},
			wantStatus: ir.NodeSkipped,
		},
		{
			name: "ValueMatchEvaluates",
			mock: stepmock.Step{Precondition: new(true)},
			conditions: func(ran string) []*ir.Condition {
				return []*ir.Condition{
					{Condition: createEmptyFileCommand(ran)},
					{Condition: "${MODE}", Expected: "prod"},
				}
			},
			wantStatus: ir.NodeSkipped,
		},
		{
			name: "EvalWithoutSubstitutionEvaluates",
			mock: stepmock.Step{Precondition: new(false)},
			conditions: func(string) []*ir.Condition {
				return []*ir.Condition{{Eval: "${MODE}", Expected: "dev"}}
			},
			wantStatus: ir.NodeSucceeded,
		},
		{
			name: "KeptAction",
			mock: stepmock.Step{Precondition: new(true), KeepAction: true},
			conditions: func(ran string) []*ir.Condition {
				return []*ir.Condition{{Condition: createEmptyFileCommand(ran), Negate: true}}
			},
			wantStatus: ir.NodeSucceeded,
			wantAction: true,
		},
		{
			name: "UnscriptedRuns",
			mock: stepmock.Step{Real: true},
			conditions: func(ran string) []*ir.Condition {
				return []*ir.Condition{{Condition: createEmptyFileCommand(ran)}}
			},
			wantStatus: ir.NodeSucceeded,
			wantAction: true,
			wantRan:    true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			ran := filepath.Join(dir, "ran")
			action := filepath.Join(dir, "action")
			r := setupRunner(t, withStepMocks(map[string]stepmock.Step{
				"gate":  tc.mock,
				"after": {},
			}))
			gate := newStep("gate", withEnvVars("MODE=dev"), withCommand(createEmptyFileCommand(action)))
			gate.Preconditions = tc.conditions(ran)
			wantAfter := ir.NodeSucceeded
			if tc.wantStatus == ir.NodeSkipped {
				wantAfter = ir.NodeSkipped
			}
			result := r.newPlan(t, gate, successStep("after", "gate")).assertRun(t, ir.Succeeded)

			result.assertNodeStatus(t, "gate", tc.wantStatus)
			result.assertNodeStatus(t, "after", wantAfter)
			assert.Equal(t, tc.wantAction, fileutil.FileExists(action), "action ran")
			assert.Equal(t, tc.wantRan, fileutil.FileExists(ran), "condition command ran")
		})
	}

	// The precondition is decided once for the step start, and each retry
	// takes the next result.
	t.Run("OncePerStart", func(t *testing.T) {
		t.Parallel()

		ran := filepath.Join(t.TempDir(), "ran")
		r := setupRunner(t, withStepMocks(map[string]stepmock.Step{
			"deploy": {
				Precondition: new(true),
				Results:      []stepmock.Result{{ExitCode: 1}, {}},
			},
		}))
		deploy := newStep("deploy", withCommand("exit 0"), withRetryPolicy(1, 0),
			withPrecondition(&ir.Condition{Condition: createEmptyFileCommand(ran), Negate: true}))
		result := r.newPlan(t, deploy).assertRun(t, ir.Succeeded)

		result.assertNodeStatus(t, "deploy", ir.NodeSucceeded)
		assert.Equal(t, 2, executions(t, result.Plan, "deploy"))
		assert.NoFileExists(t, ran)
	})

	t.Run("Handler", func(t *testing.T) {
		t.Parallel()

		ran := filepath.Join(t.TempDir(), "ran")
		handler := newHandlerStep(t, "onFailure", "", "exit 0")
		handler.Preconditions = []*ir.Condition{{Condition: createEmptyFileCommand(ran)}}
		r := setupRunner(t, withOnFailure(handler), withFailureHandlerMock(map[string]stepmock.Step{
			"build": {Results: []stepmock.Result{{ExitCode: 1}}},
		}, stepmock.Step{Precondition: new(false)}))
		r.newPlan(t, successStep("build")).assertRun(t, ir.Failed)

		assert.Equal(t, ir.NodeSkipped, r.runner.HandlerNode(ir.HandlerOnFailure).State().Status)
		assert.NoFileExists(t, ran)
	})
}

// The scripted DAG-level precondition decides the DAG-level conditions that
// run a command. Not met aborts the run before any step starts.
func TestStepMockDAGPrecondition(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		met  bool
		// conditions builds the DAG's preconditions. A command of one that
		// runs creates ranPath.
		conditions func(ranPath string) []*ir.Condition
		wantRun    ir.Status
	}{
		{
			name: "NotMetAborts",
			met:  false,
			conditions: func(ran string) []*ir.Condition {
				return []*ir.Condition{{Condition: createEmptyFileCommand(ran)}}
			},
			wantRun: ir.Aborted,
		},
		{
			name: "MetPassesNegated",
			met:  true,
			conditions: func(ran string) []*ir.Condition {
				return []*ir.Condition{{Condition: createEmptyFileCommand(ran), Negate: true}}
			},
			wantRun: ir.Succeeded,
		},
		{
			name: "ValueMatchEvaluates",
			met:  true,
			conditions: func(ran string) []*ir.Condition {
				return []*ir.Condition{
					{Condition: createEmptyFileCommand(ran)},
					{Condition: "staging", Expected: "production"},
				}
			},
			wantRun: ir.Aborted,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ran := filepath.Join(t.TempDir(), "ran")
			set := stepmock.New(map[string]stepmock.Step{"main": {}}, nil)
			set.SetDAGPrecondition(tc.met)
			r := setupRunner(t, withStepMockSet(set))
			plan := r.newPlan(t, successStep("main"))
			ctx := dagPreconditionContext(r, plan, tc.conditions(ran)...)

			require.NoError(t, r.runner.Run(ctx, plan.Plan, nil))

			assert.Equal(t, tc.wantRun, r.runner.Status(ctx, plan.Plan))
			wantMain := ir.NodeSucceeded
			if tc.wantRun == ir.Aborted {
				wantMain = ir.NodeNotStarted
			}
			assert.Equal(t, wantMain, plan.GetNodeByName("main").State().Status)
			assert.NoFileExists(t, ran)
		})
	}
}

// A repeat condition that runs a command takes the condition of the result
// each execution used, and its command does not run. A value-match repeat
// condition evaluates as written. The limits keep a regression from looping
// without end.
func TestStepMockRepeatCondition(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		// policy builds the step's repeat policy. A condition command that
		// runs creates ranPath.
		policy         func(ranPath string) ir.RepeatPolicy
		results        []stepmock.Result
		wantRun        ir.Status
		wantStatus     ir.NodeStatus
		wantExecutions int
	}{
		{
			name: "While",
			policy: func(ran string) ir.RepeatPolicy {
				return ir.RepeatPolicy{RepeatMode: ir.RepeatModeWhile, Limit: 10,
					Condition: &ir.Condition{Condition: createEmptyFileCommand(ran)}}
			},
			results: []stepmock.Result{
				{Condition: new(true)}, {Condition: new(true)}, {Condition: new(false)},
			},
			wantRun:        ir.Succeeded,
			wantStatus:     ir.NodeSucceeded,
			wantExecutions: 3,
		},
		{
			name: "Until",
			policy: func(ran string) ir.RepeatPolicy {
				return ir.RepeatPolicy{RepeatMode: ir.RepeatModeUntil, Limit: 10,
					Condition: &ir.Condition{Condition: createEmptyFileCommand(ran)}}
			},
			results:        []stepmock.Result{{Condition: new(false)}, {Condition: new(true)}},
			wantRun:        ir.Succeeded,
			wantStatus:     ir.NodeSucceeded,
			wantExecutions: 2,
		},
		{
			// The final check at the limit takes the last execution's result.
			name: "OnLimitFail",
			policy: func(ran string) ir.RepeatPolicy {
				return ir.RepeatPolicy{RepeatMode: ir.RepeatModeUntil, Limit: 2, OnLimit: ir.RepeatOnLimitFail,
					Condition: &ir.Condition{Condition: createEmptyFileCommand(ran)}}
			},
			results:        []stepmock.Result{{Condition: new(false)}},
			wantRun:        ir.Failed,
			wantStatus:     ir.NodeFailed,
			wantExecutions: 2,
		},
		{
			name: "ValueMatchEvaluates",
			policy: func(string) ir.RepeatPolicy {
				return ir.RepeatPolicy{RepeatMode: ir.RepeatModeWhile, Limit: 3,
					Condition: &ir.Condition{Condition: "pending", Expected: "pending"}}
			},
			results:        []stepmock.Result{{Condition: new(false)}},
			wantRun:        ir.Succeeded,
			wantStatus:     ir.NodeSucceeded,
			wantExecutions: 3,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ran := filepath.Join(t.TempDir(), "ran")
			r := setupRunner(t, withStepMocks(map[string]stepmock.Step{"poll": {Results: tc.results}}))
			poll := newStep("poll", withCommand("exit 0"))
			poll.RepeatPolicy = tc.policy(ran)
			result := r.newPlan(t, poll).assertRun(t, tc.wantRun)

			result.assertNodeStatus(t, "poll", tc.wantStatus)
			assert.Equal(t, tc.wantExecutions, executions(t, result.Plan, "poll"))
			assert.NoFileExists(t, ran)
		})
	}
}

// A mocked step that repeats without a limit runs one execution per result
// and then neither repeats nor retries, unless its own repeat decision stops
// it first. It ends with the status of its last execution. The run timeout
// ends a regression that repeats without end.
func TestStepMockContinuous(t *testing.T) {
	t.Parallel()

	failed := stepmock.Result{ExitCode: 1}
	for _, tc := range []struct {
		name           string
		mode           ir.RepeatMode
		retries        int
		results        []stepmock.Result
		wantRun        ir.Status
		wantStatus     ir.NodeStatus
		wantExecutions int
	}{
		{
			name:           "StopsAfterResults",
			mode:           ir.RepeatModeWhile,
			results:        []stepmock.Result{{}, {}, {}},
			wantRun:        ir.Succeeded,
			wantStatus:     ir.NodeSucceeded,
			wantExecutions: 3,
		},
		{
			name:           "NoResults",
			mode:           ir.RepeatModeWhile,
			wantRun:        ir.Succeeded,
			wantStatus:     ir.NodeSucceeded,
			wantExecutions: 1,
		},
		{
			name:           "RepeatDecisionFirst",
			mode:           ir.RepeatModeWhile,
			results:        []stepmock.Result{{}, failed, {}},
			wantRun:        ir.Failed,
			wantStatus:     ir.NodeFailed,
			wantExecutions: 2,
		},
		{
			name:           "LastStatus",
			mode:           ir.RepeatModeUntil,
			results:        []stepmock.Result{failed, failed},
			wantRun:        ir.Failed,
			wantStatus:     ir.NodeFailed,
			wantExecutions: 2,
		},
		{
			name:           "NoRetryAfterResults",
			mode:           ir.RepeatModeWhile,
			retries:        3,
			results:        []stepmock.Result{{}, failed},
			wantRun:        ir.Failed,
			wantStatus:     ir.NodeFailed,
			wantExecutions: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := setupRunner(t, withTimeout(time.Minute),
				withStepMocks(map[string]stepmock.Step{"beat": {Results: tc.results}}))
			beat := newStep("beat", withCommand("exit 0"), withRetryPolicy(tc.retries, 0))
			beat.RepeatPolicy.RepeatMode = tc.mode
			result := r.newPlan(t, beat).assertRun(t, tc.wantRun)

			result.assertNodeStatus(t, "beat", tc.wantStatus)
			assert.Equal(t, tc.wantExecutions, executions(t, result.Plan, "beat"))
		})
	}
}

// A repeat limit from a reference is decided when it resolves. A positive
// value bounds the step as written. A value that leaves the step without a
// limit makes it continuous: a mock's results bound it, a real step repeats
// as written, and any other step stops the run before it starts.
func TestStepMockRepeatLimitReference(t *testing.T) {
	t.Parallel()

	repeatLimit := func(value string) stepOption {
		return func(step *ir.Step) {
			step.Env = append(step.Env, "N="+value)
			step.RepeatPolicy.RepeatMode = ir.RepeatModeWhile
			step.RepeatPolicy.LimitStr = "${N}"
		}
	}

	t.Run("PositiveBoundsAsWritten", func(t *testing.T) {
		t.Parallel()

		r := setupRunner(t, withStepMocks(map[string]stepmock.Step{"poll": {}}))
		result := r.newPlan(t, newStep("poll", withCommand("exit 0"), repeatLimit("3"))).assertRun(t, ir.Succeeded)

		assert.Equal(t, 3, executions(t, result.Plan, "poll"))
	})

	t.Run("ZeroBoundsByResults", func(t *testing.T) {
		t.Parallel()

		r := setupRunner(t, withTimeout(time.Minute), withStepMocks(map[string]stepmock.Step{
			"poll": {Results: []stepmock.Result{{}, {}}},
		}))
		result := r.newPlan(t, newStep("poll", withCommand("exit 0"), repeatLimit("0"))).assertRun(t, ir.Succeeded)

		assert.Equal(t, 2, executions(t, result.Plan, "poll"))
	})

	// The real step fails on its third execution, which ends its while loop.
	t.Run("ZeroRealRepeatsAsWritten", func(t *testing.T) {
		t.Parallel()

		counter := filepath.Join(t.TempDir(), "count")
		r := setupRunner(t, withTimeout(time.Minute), withStepMocks(map[string]stepmock.Step{
			"poll": {Real: true},
		}))
		poll := newStep("poll", withScript(counterThresholdExitScript(counter, 2, 0, 1)), repeatLimit("0"))
		result := r.newPlan(t, poll).assertRun(t, ir.Failed)

		assert.Equal(t, 3, executions(t, result.Plan, "poll"))
	})

	for _, tc := range []struct {
		name  string
		steps map[string]stepmock.Step
	}{
		{name: "ZeroStopsUnmocked"},
		{name: "ZeroStopsKeptAction", steps: map[string]stepmock.Step{"tick": {KeepAction: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			marker := filepath.Join(t.TempDir(), "ran")
			r := setupRunner(t, withTimeout(time.Minute), withStepMocks(tc.steps))
			tick := newStep("tick", withCommand(createEmptyFileCommand(marker)), repeatLimit("0"))
			plan := r.newPlan(t, tick, successStep("after", "tick"))
			ctx := dagPreconditionContext(r, plan)

			err := r.runner.Run(ctx, plan.Plan, nil)

			var unbounded *runtime.UnboundedRepeatError
			require.ErrorAs(t, err, &unbounded)
			assert.Equal(t, runtime.UnboundedRepeatError{Step: "tick", Limit: 0}, *unbounded)
			assert.EqualError(t, unbounded, "repeat limit 0 leaves the step continuous; mock it with results")
			assert.Equal(t, ir.Aborted, r.runner.Status(ctx, plan.Plan))
			assert.Equal(t, ir.NodeFailed, plan.GetNodeByName("tick").State().Status)
			assert.NoFileExists(t, marker)
		})
	}
}
