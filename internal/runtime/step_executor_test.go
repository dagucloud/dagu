// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	runtimeexec "github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/stretchr/testify/require"
)

type sideChannelExecutor struct {
	inputMessages     []exec.LLMMessage
	pushBackInputs    map[string]string
	pushBackIteration int
	previousStdout    string
	closed            bool
	toolDefinitions   []exec.ToolDefinition
	messages          []exec.LLMMessage
	subRuns           []exec.SubDAGRun
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
func (e *sideChannelExecutor) GetToolDefinitions() []exec.ToolDefinition {
	return append([]exec.ToolDefinition(nil), e.toolDefinitions...)
}
func (e *sideChannelExecutor) GetOutputs() map[string]any {
	return e.outputs
}

func TestStepExecutorCapturesExecutorSideChannels(t *testing.T) {
	executorType := "test-step-executor-side-channels"
	execCh := make(chan *sideChannelExecutor, 1)
	runtimeexec.RegisterExecutor(executorType, func(context.Context, core.Step) (runtimeexec.Executor, error) {
		exec := &sideChannelExecutor{
			messages: []exec.LLMMessage{
				{Role: exec.RoleAssistant, Content: "new message"},
			},
			subRuns: []exec.SubDAGRun{
				{DAGRunID: "new-run", DAGName: "new-dag", Params: "NEW=1"},
			},
			toolDefinitions: []exec.ToolDefinition{
				{Name: "lookup", Description: "look up data"},
			},
			outputs: map[string]any{"answer": float64(42)},
		}
		execCh <- exec
		return exec, nil
	}, nil, core.ExecutorCapabilities{})
	t.Cleanup(func() { runtimeexec.UnregisterExecutor(executorType) })

	node := runtime.NewNode(core.Step{
		Name: "side-channel-step",
		ExecutorConfig: core.ExecutorConfig{
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
	ctx := runtime.NewContext(context.Background(), &core.DAG{}, "run-1", "dag.log")
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
	require.Equal(t, []exec.ToolDefinition{{Name: "lookup", Description: "look up data"}}, node.GetToolDefinitions())
	require.NotNil(t, state.OutputsValue)
	require.JSONEq(t, `{"answer":42}`, *state.OutputsValue)
}
