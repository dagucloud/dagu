// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	runtimeexec "github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/stretchr/testify/require"
)

type emptySideChannelExecutor struct{}

func (e *emptySideChannelExecutor) SetStdout(io.Writer)  {}
func (e *emptySideChannelExecutor) SetStderr(io.Writer)  {}
func (e *emptySideChannelExecutor) Kill(os.Signal) error { return nil }
func (e *emptySideChannelExecutor) Run(context.Context) error {
	return nil
}
func (e *emptySideChannelExecutor) GetToolDefinitions() []exec.ToolDefinition {
	return nil
}
func (e *emptySideChannelExecutor) GetOutputs() map[string]any {
	return nil
}

func TestStepExecutorReturnsWrappedSetupError(t *testing.T) {
	executorType := "test-step-executor-setup-error"
	setupErr := errors.New("setup failed")
	runtimeexec.RegisterExecutor(executorType, func(context.Context, core.Step) (runtimeexec.Executor, error) {
		return nil, setupErr
	}, nil, core.ExecutorCapabilities{})
	t.Cleanup(func() { runtimeexec.UnregisterExecutor(executorType) })

	node := NewNode(core.Step{
		Name: "setup-error-step",
		ExecutorConfig: core.ExecutorConfig{
			Type: executorType,
		},
	}, NodeState{})

	err := NewStepExecutor().Execute(newTestStepExecutorContext(), node)
	require.ErrorIs(t, err, setupErr)
	require.Same(t, err, node.Error())

	var wrapped *stepSetupError
	require.ErrorAs(t, err, &wrapped)
}

func TestStepExecutorClearsEmptyToolDefinitionsAndOutputs(t *testing.T) {
	executorType := "test-step-executor-empty-side-channels"
	runtimeexec.RegisterExecutor(executorType, func(context.Context, core.Step) (runtimeexec.Executor, error) {
		return &emptySideChannelExecutor{}, nil
	}, nil, core.ExecutorCapabilities{})
	t.Cleanup(func() { runtimeexec.UnregisterExecutor(executorType) })

	node := NewNode(core.Step{
		Name: "empty-side-channel-step",
		ExecutorConfig: core.ExecutorConfig{
			Type: executorType,
		},
	}, NodeState{})
	node.SetToolDefinitions([]exec.ToolDefinition{{Name: "stale-tool"}})
	node.setOutputsValue(`{"stale":true}`)

	require.NoError(t, NewStepExecutor().Execute(newTestStepExecutorContext(), node))

	require.Empty(t, node.GetToolDefinitions())
	require.Nil(t, node.State().OutputsValue)
}

func newTestStepExecutorContext() context.Context {
	return NewContext(context.Background(), &core.DAG{}, "run-1", "dag.log")
}
