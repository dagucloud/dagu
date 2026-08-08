// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	runtimeexec "github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/stretchr/testify/require"
)

const periodicFlushExecutorType = "test-step-executor-periodic-flush"

type sideChannelExecutor struct {
	inputMessages     []exec.LLMMessage
	pushBackInputs    map[string]string
	pushBackIteration int
	previousStdout    string
	closed            bool
	toolDefinitions   []exec.ToolDefinition
	messages          []exec.LLMMessage
	subRuns           []exec.SubDAGRun
	statusDetails     []exec.NodeStatusDetail
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
func (e *sideChannelExecutor) SetContext(messages []exec.LLMMessage) {
	e.inputMessages = append([]exec.LLMMessage(nil), messages...)
}
func (e *sideChannelExecutor) GetMessages() []exec.LLMMessage {
	return append([]exec.LLMMessage(nil), e.messages...)
}
func (e *sideChannelExecutor) SetPushBackContext(inputs map[string]string, iteration int) {
	e.pushBackInputs = inputs
	e.pushBackIteration = iteration
}
func (e *sideChannelExecutor) SetPushBackPreviousStdout(path string) {
	e.previousStdout = path
}
func (e *sideChannelExecutor) GetSubRuns() []exec.SubDAGRun {
	return append([]exec.SubDAGRun(nil), e.subRuns...)
}
func (e *sideChannelExecutor) GetStatusDetails() []exec.NodeStatusDetail {
	return append([]exec.NodeStatusDetail(nil), e.statusDetails...)
}
func (e *sideChannelExecutor) GetToolDefinitions() []exec.ToolDefinition {
	return append([]exec.ToolDefinition(nil), e.toolDefinitions...)
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
	if streamType == exec.StreamTypeStdout {
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
	}, nil, ir.ExecutorCapabilities{})
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
			messages: []exec.LLMMessage{
				{Role: exec.RoleAssistant, Content: "new message"},
			},
			subRuns: []exec.SubDAGRun{
				{DAGRunID: "new-run", DAGName: "new-dag", Params: "NEW=1"},
			},
			statusDetails: []exec.NodeStatusDetail{
				{Label: "customer-a", Status: ir.NodeFailed},
			},
			toolDefinitions: []exec.ToolDefinition{
				{Name: "lookup", Description: "look up data"},
			},
			outputs: map[string]any{"answer": float64(42)},
		}
		execCh <- exec
		return exec, nil
	}, nil, ir.ExecutorCapabilities{})
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
		ChatMessages: []exec.LLMMessage{
			{Role: exec.RoleUser, Content: "previous message"},
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
	require.Equal(t, []exec.LLMMessage{{Role: exec.RoleUser, Content: "previous message"}}, fakeExec.inputMessages)
	require.Equal(t, map[string]string{"reason": "try again"}, fakeExec.pushBackInputs)
	require.Equal(t, 2, fakeExec.pushBackIteration)
	require.Equal(t, "/tmp/previous.out", fakeExec.previousStdout)

	require.Equal(t, []exec.LLMMessage{{Role: exec.RoleAssistant, Content: "new message"}}, node.GetChatMessages())
	state := node.State()
	require.Equal(t, []runtime.SubDAGRun{{DAGRunID: "new-run", DAGName: "new-dag", Params: "NEW=1"}}, state.SubRuns)
	require.Equal(t, []runtime.SubDAGRun{{DAGRunID: "old-run", DAGName: "old-dag", Params: "OLD=1"}}, state.SubRunsRepeated)
	require.Equal(t, []exec.NodeStatusDetail{{Label: "customer-a", Status: ir.NodeFailed}}, state.StatusDetails)
	require.Equal(t, []exec.ToolDefinition{{Name: "lookup", Description: "look up data"}}, node.GetToolDefinitions())
	require.NotNil(t, state.OutputsValue)
	require.JSONEq(t, `{"answer":42}`, *state.OutputsValue)
}
