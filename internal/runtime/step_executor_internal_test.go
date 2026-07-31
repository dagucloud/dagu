// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	runtimeexec "github.com/dagucloud/dagu/v2/internal/runtime/executor"
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

type declaredSideChannelExecutor struct {
	emptySideChannelExecutor
}

func (e *declaredSideChannelExecutor) GetOutputs() map[string]any {
	return map[string]any{"path": "/tmp/worktree", "created": true}
}

func (e *declaredSideChannelExecutor) PublishesDeclaredOutputs() bool {
	return true
}

type invalidDeclaredSideChannelExecutor struct {
	emptySideChannelExecutor
}

func (e *invalidDeclaredSideChannelExecutor) GetOutputs() map[string]any {
	return map[string]any{"invalid": make(chan int)}
}

func (e *invalidDeclaredSideChannelExecutor) PublishesDeclaredOutputs() bool {
	return true
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

func TestStepExecutorPublishesExecutorDeclaredOutputs(t *testing.T) {
	executorType := "test-step-executor-declared-outputs"
	runtimeexec.RegisterExecutor(executorType, func(context.Context, core.Step) (runtimeexec.Executor, error) {
		return &declaredSideChannelExecutor{}, nil
	}, nil, core.ExecutorCapabilities{})
	t.Cleanup(func() { runtimeexec.UnregisterExecutor(executorType) })

	node := NewNode(core.Step{
		ID:   "worktree",
		Name: "worktree",
		ExecutorConfig: core.ExecutorConfig{
			Type: executorType,
		},
	}, NodeState{})

	require.NoError(t, NewStepExecutor().Execute(newTestStepExecutorContext(), node))
	state := node.State()
	require.NotNil(t, state.OutputsValue)
	require.NotNil(t, state.StepOutputsValue)
	require.JSONEq(t, `{"created":true,"path":"/tmp/worktree"}`, *state.StepOutputsValue)
	info := node.StepInfo()
	require.NotNil(t, info.DeclaredOutputs)
}

func TestStepExecutorRecordsDeclaredOutputSerializationError(t *testing.T) {
	executorType := "test-step-executor-invalid-declared-outputs"
	runtimeexec.RegisterExecutor(executorType, func(context.Context, core.Step) (runtimeexec.Executor, error) {
		return &invalidDeclaredSideChannelExecutor{}, nil
	}, nil, core.ExecutorCapabilities{})
	t.Cleanup(func() { runtimeexec.UnregisterExecutor(executorType) })

	node := NewNode(core.Step{
		ID:   "worktree",
		Name: "worktree",
		ExecutorConfig: core.ExecutorConfig{
			Type: executorType,
		},
	}, NodeState{})

	err := NewStepExecutor().Execute(newTestStepExecutorContext(), node)
	require.ErrorContains(t, err, "failed to serialize outputs")
	require.Same(t, err, node.Error())
}

func TestStepExecutorRecordsTimeoutBeforeCommandStarts(t *testing.T) {
	executorType := "test-step-executor-pre-run-timeout"
	runtimeexec.RegisterExecutor(executorType, func(ctx context.Context, _ core.Step) (runtimeexec.Executor, error) {
		<-ctx.Done()
		return &emptySideChannelExecutor{}, nil
	}, nil, core.ExecutorCapabilities{})
	t.Cleanup(func() { runtimeexec.UnregisterExecutor(executorType) })

	node := NewNode(core.Step{
		Name: "pre-run-timeout-step",
		ExecutorConfig: core.ExecutorConfig{
			Type: executorType,
		},
		Timeout: 10 * time.Millisecond,
	}, NodeState{})

	err := NewStepExecutor().Execute(newTestStepExecutorContext(), node)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "step timed out")
	require.Equal(t, core.NodeFailed, node.Status())
	require.Equal(t, 124, node.GetExitCode())
}

func newTestStepExecutorContext() context.Context {
	return NewContext(context.Background(), &core.DAG{}, "run-1", "dag.log")
}
