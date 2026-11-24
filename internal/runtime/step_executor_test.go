// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/runctx"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	runtimeexec "github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/runtime/stepmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const periodicFlushExecutorType = "test-step-executor-periodic-flush"

type sideChannelExecutor struct {
	inputMessages     []ir.LLMMessage
	pushBackInputs    map[string]string
	pushBackIteration int
	previousStdout    string
	closed            bool
	toolDefinitions   []ir.ToolDefinition
	messages          []ir.LLMMessage
	subRuns           []ir.SubDAGRun
	statusDetails     []ir.NodeStatusDetail
	outputs           map[string]any
	stdout            io.Writer
	stderr            io.Writer
}

func (e *sideChannelExecutor) SetStdout(out io.Writer) { e.stdout = out }
func (e *sideChannelExecutor) SetStderr(out io.Writer) { e.stderr = out }
func (e *sideChannelExecutor) Kill(os.Signal) error    { return nil }
func (e *sideChannelExecutor) Run(context.Context) error {
	return nil
}
func (e *sideChannelExecutor) Close() error {
	e.closed = true
	return nil
}
func (e *sideChannelExecutor) SetContext(messages []ir.LLMMessage) {
	e.inputMessages = append([]ir.LLMMessage(nil), messages...)
}
func (e *sideChannelExecutor) GetMessages() []ir.LLMMessage {
	return append([]ir.LLMMessage(nil), e.messages...)
}
func (e *sideChannelExecutor) SetPushBackContext(inputs map[string]string, iteration int) {
	e.pushBackInputs = inputs
	e.pushBackIteration = iteration
}
func (e *sideChannelExecutor) SetPushBackPreviousStdout(path string) {
	e.previousStdout = path
}
func (e *sideChannelExecutor) GetSubRuns() []ir.SubDAGRun {
	return append([]ir.SubDAGRun(nil), e.subRuns...)
}
func (e *sideChannelExecutor) GetStatusDetails() []ir.NodeStatusDetail {
	return append([]ir.NodeStatusDetail(nil), e.statusDetails...)
}
func (e *sideChannelExecutor) GetToolDefinitions() []ir.ToolDefinition {
	return append([]ir.ToolDefinition(nil), e.toolDefinitions...)
}
func (e *sideChannelExecutor) GetOutputs() map[string]any {
	return e.outputs
}

type periodicFlushExecutor struct {
	stdout  io.Writer
	started chan struct{}
	release <-chan struct{}
}

func (e *periodicFlushExecutor) SetStdout(out io.Writer) { e.stdout = out }
func (e *periodicFlushExecutor) SetStderr(io.Writer)     {}
func (e *periodicFlushExecutor) Kill(os.Signal) error    { return nil }
func (e *periodicFlushExecutor) Run(ctx context.Context) error {
	if _, err := io.WriteString(e.stdout, "small remote output\n"); err != nil {
		return err
	}
	close(e.started)
	select {
	case <-e.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type flushObservingWriter struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	expected []byte
	observed chan struct{}
}

func (w *flushObservingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *flushObservingWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if bytes.Contains(w.buf.Bytes(), w.expected) {
		select {
		case w.observed <- struct{}{}:
		default:
		}
	}
	return nil
}

func (w *flushObservingWriter) Close() error { return nil }

type flushObservingLogWriterFactory struct {
	stdout *flushObservingWriter
}

func (f *flushObservingLogWriterFactory) NewStepWriter(_ context.Context, _ string, streamType int) io.WriteCloser {
	if streamType == runctx.StreamTypeStdout {
		return f.stdout
	}
	return &flushObservingWriter{}
}

func TestStepExecutorPeriodicallyFlushesRemoteOutputWhileExecutorRuns(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseExecutor := func() {
		releaseOnce.Do(func() { close(release) })
	}
	defer releaseExecutor()

	runtimeexec.RegisterExecutor(periodicFlushExecutorType, func(context.Context, ir.Step) (runtimeexec.Executor, error) {
		return &periodicFlushExecutor{
			started: started,
			release: release,
		}, nil
	}, nil, registry.ExecutorCapabilities{})
	t.Cleanup(func() { runtimeexec.UnregisterExecutor(periodicFlushExecutorType) })

	writer := &flushObservingWriter{
		expected: []byte("small remote output"),
		observed: make(chan struct{}, 1),
	}
	factory := &flushObservingLogWriterFactory{stdout: writer}
	dag := &ir.DAG{Name: "periodic-flush-dag"}
	ctx := runtime.NewContext(
		context.Background(),
		dag,
		"run-1",
		"dag.log",
		runtime.WithLogWriterFactory(factory),
	)
	node := runtime.NewNode(ir.Step{
		Name: "periodic-flush-step",
		ExecutorConfig: ir.ExecutorConfig{
			Type: periodicFlushExecutorType,
		},
	}, runtime.NodeState{})
	require.NoError(t, node.Prepare(ctx, t.TempDir(), "run-1"))
	defer func() { require.NoError(t, node.Teardown()) }()

	executionDone := make(chan error, 1)
	go func() {
		executionDone <- runtime.NewStepExecutor().Execute(ctx, node)
	}()

	select {
	case <-started:
	case err := <-executionDone:
		require.FailNow(t, "executor completed before blocking", "error: %v", err)
	case <-time.After(10 * time.Second):
		require.FailNow(t, "executor did not start")
	}

	select {
	case <-writer.observed:
	case err := <-executionDone:
		require.FailNow(t, "executor completed before periodic output flush", "error: %v", err)
	case <-time.After(10 * time.Second):
		require.FailNow(t, "remote output was not flushed while executor was running")
	}

	select {
	case err := <-executionDone:
		require.FailNow(t, "executor completed before release", "error: %v", err)
	default:
	}

	releaseExecutor()
	require.NoError(t, <-executionDone)
}

func TestStepExecutorCapturesExecutorSideChannels(t *testing.T) {
	executorType := "test-step-executor-side-channels"
	execCh := make(chan *sideChannelExecutor, 1)
	runtimeexec.RegisterExecutor(executorType, func(context.Context, ir.Step) (runtimeexec.Executor, error) {
		exec := &sideChannelExecutor{
			messages: []ir.LLMMessage{
				{Role: ir.LLMRoleAssistant, Content: "new message"},
			},
			subRuns: []ir.SubDAGRun{
				{DAGRunID: "new-run", DAGName: "new-dag", Params: "NEW=1"},
			},
			statusDetails: []ir.NodeStatusDetail{
				{Label: "customer-a", Status: ir.NodeFailed},
			},
			toolDefinitions: []ir.ToolDefinition{
				{Name: "lookup", Description: "look up data"},
			},
			outputs: map[string]any{"answer": float64(42)},
		}
		execCh <- exec
		return exec, nil
	}, nil, registry.ExecutorCapabilities{})
	t.Cleanup(func() { runtimeexec.UnregisterExecutor(executorType) })

	node := runtime.NewNode(ir.Step{
		Name: "side-channel-step",
		ExecutorConfig: ir.ExecutorConfig{
			Type: executorType,
		},
	}, runtime.NodeState{
		ApprovalIteration:      2,
		PushBackInputs:         map[string]string{"reason": "try again"},
		PushBackPreviousStdout: "/tmp/previous.out",
		ChatMessages: []ir.LLMMessage{
			{Role: ir.LLMRoleUser, Content: "previous message"},
		},
	})
	node.SetRepeated(true)
	node.SetSubRuns([]runtime.SubDAGRun{
		{DAGRunID: "old-run", DAGName: "old-dag", Params: "OLD=1"},
	})

	stepExecutor := runtime.NewStepExecutor()
	ctx := runtime.NewContext(context.Background(), &ir.DAG{}, "run-1", "dag.log")
	require.NoError(t, stepExecutor.Execute(ctx, node))

	fakeExec := <-execCh
	require.True(t, fakeExec.closed)
	require.Equal(t, []ir.LLMMessage{{Role: ir.LLMRoleUser, Content: "previous message"}}, fakeExec.inputMessages)
	require.Equal(t, map[string]string{"reason": "try again"}, fakeExec.pushBackInputs)
	require.Equal(t, 2, fakeExec.pushBackIteration)
	require.Equal(t, "/tmp/previous.out", fakeExec.previousStdout)

	require.Equal(t, []ir.LLMMessage{{Role: ir.LLMRoleAssistant, Content: "new message"}}, node.GetChatMessages())
	state := node.State()
	require.Equal(t, []runtime.SubDAGRun{{DAGRunID: "new-run", DAGName: "new-dag", Params: "NEW=1"}}, state.SubRuns)
	require.Equal(t, []runtime.SubDAGRun{{DAGRunID: "old-run", DAGName: "old-dag", Params: "OLD=1"}}, state.SubRunsRepeated)
	require.Equal(t, []ir.NodeStatusDetail{{Label: "customer-a", Status: ir.NodeFailed}}, state.StatusDetails)
	require.Equal(t, []ir.ToolDefinition{{Name: "lookup", Description: "look up data"}}, node.GetToolDefinitions())
	require.NotNil(t, state.OutputsValue)
	require.JSONEq(t, `{"answer":42}`, *state.OutputsValue)
}

// A failed attempt publishes no outputs, including those its executor reports
// when it fails: a dag.run step whose child run fails adds none of the
// child's outputs to the outputs the run collects, and a later reference
// keeps its text. The later step is mocked so that no shell runs that text.
func TestFailedChildRunPublishesNoOutputs(t *testing.T) {
	t.Parallel()

	r := setupRunner(t, withStepMocks(map[string]stepmock.Step{"verify": {}}))
	r.Context = runtimeexec.WithSubWorkflowRunner(r.Context, &childRuns{
		outputs: map[string]any{"version": "v1.2.3"},
		err:     errors.New("child run failed"),
	})
	rollout := newStep("rollout", withID("rollout"), withExecutorType(ir.ExecutorTypeDAG), withSubDAG("deploy"),
		withContinueOn(ir.ContinueOn{Failure: true}))
	verify := newStep("verify", withDepends("rollout"),
		withCommand("./verify.sh ${rollout.outputs.version} ${steps.rollout.outputs.version}"))
	ph := r.newPlan(t, rollout, verify)
	ph.steps = []ir.Step{rollout, verify}
	ph.localDAGs = map[string]*ir.DAG{
		"deploy": {Name: "deploy", YamlData: []byte("name: deploy\nsteps:\n  - run: ./deploy.sh\n")},
	}
	ph.contextOpts = []runtime.ContextOption{
		runtime.WithRootDAGRun(runtime.NewDAGRunRef("test_dag", r.cfg.DAGRunID)),
	}
	result := ph.assertRun(t, ir.PartiallySucceeded)

	result.assertNodeStatus(t, "rollout", ir.NodeFailed)
	assert.Nil(t, result.nodeByName(t, "rollout").NodeData().OutputsValueStringMap())
	assert.Equal(t, []string{"${rollout.outputs.version}", "${steps.rollout.outputs.version}"},
		result.nodeByName(t, "verify").Step().Commands[0].Args)
}

// Push-back-aware executors receive the inputs the step may see: an approval
// allowlist limits approval push-back inputs but not human-task feedback.
func TestStepExecutorScopesPushBackInputs(t *testing.T) {
	executorType := "test-step-executor-push-back-scope"
	execCh := make(chan *sideChannelExecutor, 2)
	runtimeexec.RegisterExecutor(executorType, func(context.Context, ir.Step) (runtimeexec.Executor, error) {
		exec := &sideChannelExecutor{}
		execCh <- exec
		return exec, nil
	}, nil, registry.ExecutorCapabilities{})
	t.Cleanup(func() { runtimeexec.UnregisterExecutor(executorType) })

	inputs := map[string]string{"FEEDBACK": "tighten", "feedback": "add tests"}
	for _, tc := range []struct {
		humanTask bool
		want      map[string]string
	}{
		{humanTask: false, want: map[string]string{"FEEDBACK": "tighten"}},
		{humanTask: true, want: inputs},
	} {
		node := runtime.NewNode(ir.Step{
			Name:           "draft",
			ExecutorConfig: ir.ExecutorConfig{Type: executorType},
			Approval:       &ir.ApprovalConfig{Input: []string{"FEEDBACK"}},
		}, runtime.NodeState{
			ApprovalIteration:      1,
			PushBackInputs:         inputs,
			PushBackHistory:        []ir.PushBackEntry{{Iteration: 1, Inputs: inputs, HumanTask: tc.humanTask}},
			PushBackPreviousStdout: "/logs/draft.out",
		})

		ctx := runtime.NewContext(context.Background(), &ir.DAG{}, "run-1", "dag.log")
		require.NoError(t, runtime.NewStepExecutor().Execute(ctx, node))

		fakeExec := <-execCh
		require.Equal(t, tc.want, fakeExec.pushBackInputs, "humanTask=%v", tc.humanTask)
		require.Equal(t, 1, fakeExec.pushBackIteration)
	}
}

// A step in the rewound scope that has not run yet has no work for feedback to
// revise. The executor must not rewrite its prompt, though the step's
// environment still carries the push-back context.
func TestStepExecutorKeepsPushBackFromStepsThatNeverRan(t *testing.T) {
	executorType := "test-step-executor-push-back-first-run"
	execCh := make(chan *sideChannelExecutor, 1)
	runtimeexec.RegisterExecutor(executorType, func(context.Context, ir.Step) (runtimeexec.Executor, error) {
		exec := &sideChannelExecutor{}
		execCh <- exec
		return exec, nil
	}, nil, registry.ExecutorCapabilities{})
	t.Cleanup(func() { runtimeexec.UnregisterExecutor(executorType) })

	inputs := map[string]string{"feedback": "add tests"}
	node := runtime.NewNode(ir.Step{
		Name:           "publish",
		ExecutorConfig: ir.ExecutorConfig{Type: executorType},
	}, runtime.NodeState{
		ApprovalIteration: 1,
		PushBackInputs:    inputs,
		PushBackHistory:   []ir.PushBackEntry{{Iteration: 1, Inputs: inputs, HumanTask: true}},
	})

	ctx := runtime.NewContext(context.Background(), &ir.DAG{}, "run-1", "dag.log")
	require.NoError(t, runtime.NewStepExecutor().Execute(ctx, node))

	fakeExec := <-execCh
	require.Zero(t, fakeExec.pushBackIteration)
	require.Empty(t, fakeExec.pushBackInputs)
	require.Empty(t, fakeExec.previousStdout)
}

// meteredChildRuns stands for child runs that call models. The child of the
// item "fast" ends at once with its model usage; the others wait to be
// released.
type meteredChildRuns struct {
	childRuns
	release <-chan struct{}
}

func (c *meteredChildRuns) Run(ctx context.Context, req runtimeexec.SubWorkflowRequest) (*ir.RunStatus, error) {
	status := &ir.RunStatus{Name: req.DAG.Name, DAGRunID: req.RunID, Status: ir.Succeeded}
	if req.Params == "fast" {
		status.ModelUsage = []ir.ModelUsage{{Provider: "openrouter", Model: "m", Calls: 1}}
		return status, nil
	}
	select {
	case <-c.release:
		return status, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// A parallel step reports the model usage of a child run that ended while
// its other children still run.
func TestParallelStepReportsModelUsageWhileRunning(t *testing.T) {
	runtime.SetLiveModelUsageInterval(t, 10*time.Millisecond)

	release := make(chan struct{})
	child := &ir.DAG{Name: "child", YamlData: []byte("name: child\nsteps:\n  - run: \"true\"\n")}
	dag := &ir.DAG{Name: "root", LocalDAGs: map[string]*ir.DAG{child.Name: child}}
	step := ir.Step{
		Name:           "fan-out",
		ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeParallel},
		SubDAG:         &ir.SubDAG{Name: child.Name},
		Parallel:       &ir.ParallelConfig{Items: []ir.ParallelItem{{Value: "fast"}, {Value: "slow"}}},
	}
	ctx := runtime.NewContext(
		runtimeexec.WithSubWorkflowRunner(context.Background(), &meteredChildRuns{release: release}),
		dag,
		"run-1",
		"dag.log",
		runtime.WithRootDAGRun(runtime.NewDAGRunRef(dag.Name, "run-1")),
	)
	ctx = runtime.WithEnv(ctx, runtime.NewEnv(ctx, step))
	node := runtime.NewNode(step, runtime.NodeState{})

	reported := make(chan *ir.StepModelUsage, 1)
	done := make(chan error, 1)
	go func() {
		done <- runtime.NewStepExecutor().ExecuteWithProgress(ctx, node, nil, func() {
			select {
			case reported <- node.ModelUsage():
			default:
			}
		})
	}()

	select {
	case usage := <-reported:
		require.NotNil(t, usage)
		require.Len(t, usage.Reported, 1)
		assert.Equal(t, int64(1), usage.Reported[0].Models[0].Calls)
	case <-time.After(5 * time.Second):
		t.Fatal("the usage of the ended child was not reported while the step ran")
	}
	close(release)
	require.NoError(t, <-done)
}
