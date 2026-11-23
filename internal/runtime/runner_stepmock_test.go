// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	_ "github.com/dagucloud/dagu/v2/internal/runtime/builtin/dag"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/runtime/stepmock"
	"github.com/dagucloud/dagu/v2/internal/spec"
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

// loadSteps builds the steps of a DAG from YAML, as a run of the DAG does.
func loadSteps(t *testing.T, data string) []ir.Step {
	t.Helper()

	dag, err := spec.LoadYAML(t.Context(), []byte(data))
	require.NoError(t, err)
	return dag.Steps
}

// planSources holds a plan step for each source of named outputs that Spec
// 007 lists. A real run would not publish the mocked values from any of them:
// the mocks write stdout that is not JSON and create no file, and the action
// publishes values of its own.
var planSources = map[string]string{
	"OutputFile": `
  - id: plan
    run: ./plan.sh
    outputs:
      - name: risk
      - name: replicas
        type: json`,
	"OutputSchema": `
  - id: plan
    run: ./plan.sh
    output_schema:
      type: object
      required: [risk, replicas]
      properties:
        risk: {type: string}
        replicas: {type: integer}`,
	"ObjectFormOutput": `
  - id: plan
    run: ./plan.sh
    output:
      risk: {from: stdout, decode: json, select: .risk}
      replicas: {from: file, path: plan.json, decode: json}`,
	"StdoutOutputs": `
  - id: plan
    run: ./plan.sh
    stdout:
      outputs:
        fields:
          risk: {decode: json, select: .risk}
          replicas: {decode: json, select: .replicas}`,
	"Action": `
  - id: plan
    action: outputs.write
    with:
      values:
        risk: high
        replicas: 1`,
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
// precondition read them. A json output publishes a string as a JSON string.
func TestStepMockPublishesDeclaredOutputs(t *testing.T) {
	t.Parallel()

	r := setupRunner(t, withStepMocks(map[string]stepmock.Step{
		"plan": {Results: []stepmock.Result{{Outputs: map[string]any{
			"risk":  "low",
			"notes": "first\nEOF\nlast",
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
	assert.JSONEq(t, `{"risk":"low","notes":"first\nEOF\nlast","meta":"{\"replicas\":2}","color":"\"blue\""}`, *outputs)
}

// A successful mocked execution publishes its outputs whatever the source of
// the step's named outputs, and a later step resolves them. A value that is
// not a string publishes as its JSON text.
func TestStepMockPublishesNamedOutputs(t *testing.T) {
	t.Parallel()

	for name, plan := range planSources {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := setupRunner(t, withStepMocks(map[string]stepmock.Step{
				"plan": {Results: []stepmock.Result{{
					Stdout:  "not json\n",
					Outputs: map[string]any{"risk": "low", "replicas": 2},
				}}},
				"deploy": {},
			}))
			steps := loadSteps(t, "steps:"+plan+`
  - id: deploy
    run: ./deploy.sh ${steps.plan.outputs.risk} ${steps.plan.outputs.replicas}
`)
			result := r.newPlan(t, steps...).assertRun(t, ir.Succeeded)

			outputs := result.nodeByName(t, "plan").State().StepOutputsValue
			require.NotNil(t, outputs)
			assert.JSONEq(t, `{"risk":"low","replicas":"2"}`, *outputs)
			assert.Equal(t, []string{"low", "2"}, result.nodeByName(t, "deploy").Step().Commands[0].Args)
		})
	}
}

// A failed mocked execution publishes no outputs, whatever their source. A
// later mocked step that reads one keeps the reference as text and still
// takes its result.
func TestStepMockFailedAttemptPublishesNoOutputs(t *testing.T) {
	t.Parallel()

	for name, plan := range planSources {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := setupRunner(t, withStepMocks(map[string]stepmock.Step{
				"plan": {Results: []stepmock.Result{{ExitCode: 1, Outputs: map[string]any{"risk": "high", "replicas": 3}}}},
				"use":  {Results: []stepmock.Result{{Stdout: "used\n"}}},
			}))
			steps := loadSteps(t, "steps:"+plan+`
    continue_on: failed
  - id: use
    env:
      - RISK: ${steps.plan.outputs.risk}
    run: echo ${steps.plan.outputs.risk}
`)
			result := r.newPlan(t, steps...).assertRun(t, ir.PartiallySucceeded)

			result.assertNodeStatus(t, "plan", ir.NodeFailed)
			assert.Nil(t, result.nodeByName(t, "plan").State().StepOutputsValue)
			result.assertNodeStatus(t, "use", ir.NodeSucceeded)
			usedNode := result.nodeByName(t, "use")
			assert.Equal(t, []string{"${steps.plan.outputs.risk}"}, usedNode.Step().Commands[0].Args)
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
// starts succeeds.
type childRuns struct {
	mu     sync.Mutex
	runIDs []string
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
	return &ir.RunStatus{Name: req.DAG.Name, DAGRunID: req.RunID, Status: ir.Succeeded}, nil
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

func (n webNodes) Select(ctx context.Context, _ string) ([]inventory.ResolvedNode, error) {
	return n.Nodes(ctx)
}

func (webNodes) Nodes(context.Context) ([]inventory.ResolvedNode, error) {
	return []inventory.ResolvedNode{{Name: "web01"}, {Name: "web02"}}, nil
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
		"deploy": {Name: "deploy", YamlData: []byte("name: deploy\nsteps:\n  - run: ./deploy.sh\n")},
		"deploy-one": {
			Name:         "deploy-one",
			NodeRequired: true,
			YamlData:     []byte("name: deploy-one\nnode: required\nsteps:\n  - run: ./deploy.sh\n"),
		},
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
