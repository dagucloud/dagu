// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"

	"github.com/dagucloud/dagu/v2/internal/ir"
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
func (e *emptySideChannelExecutor) GetToolDefinitions() []ir.ToolDefinition {
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

type emptyDeclaredSideChannelExecutor struct {
	emptySideChannelExecutor
}

func (e *emptyDeclaredSideChannelExecutor) PublishesDeclaredOutputs() bool {
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

type outputsValueSideChannelExecutor struct {
	emptySideChannelExecutor
	value any
}

func (e *outputsValueSideChannelExecutor) GetOutputsValue() any {
	return e.value
}

func TestStepExecutorReturnsWrappedSetupError(t *testing.T) {
	executorType := "test-step-executor-setup-error"
	setupErr := errors.New("setup failed")
	runtimeexec.RegisterExecutor(executorType, func(context.Context, ir.Step) (runtimeexec.Executor, error) {
		return nil, setupErr
	}, nil, registry.ExecutorCapabilities{})
	t.Cleanup(func() { runtimeexec.UnregisterExecutor(executorType) })

	node := NewNode(ir.Step{
		Name: "setup-error-step",
		ExecutorConfig: ir.ExecutorConfig{
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
	runtimeexec.RegisterExecutor(executorType, func(context.Context, ir.Step) (runtimeexec.Executor, error) {
		return &emptySideChannelExecutor{}, nil
	}, nil, registry.ExecutorCapabilities{})
	t.Cleanup(func() { runtimeexec.UnregisterExecutor(executorType) })

	node := NewNode(ir.Step{
		Name: "empty-side-channel-step",
		ExecutorConfig: ir.ExecutorConfig{
			Type: executorType,
		},
	}, NodeState{})
	node.SetToolDefinitions([]ir.ToolDefinition{{Name: "stale-tool"}})
	node.setOutputsValue(`{"stale":true}`)

	require.NoError(t, NewStepExecutor().Execute(newTestStepExecutorContext(), node))

	require.Empty(t, node.GetToolDefinitions())
	require.Nil(t, node.State().OutputsValue)
}

func TestStepExecutorPublishesExecutorDeclaredOutputs(t *testing.T) {
	executorType := "test-step-executor-declared-outputs"
	runtimeexec.RegisterExecutor(executorType, func(context.Context, ir.Step) (runtimeexec.Executor, error) {
		return &declaredSideChannelExecutor{}, nil
	}, nil, registry.ExecutorCapabilities{})
	t.Cleanup(func() { runtimeexec.UnregisterExecutor(executorType) })

	node := NewNode(ir.Step{
		ID:   "worktree",
		Name: "worktree",
		ExecutorConfig: ir.ExecutorConfig{
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
	runtimeexec.RegisterExecutor(executorType, func(context.Context, ir.Step) (runtimeexec.Executor, error) {
		return &invalidDeclaredSideChannelExecutor{}, nil
	}, nil, registry.ExecutorCapabilities{})
	t.Cleanup(func() { runtimeexec.UnregisterExecutor(executorType) })

	node := NewNode(ir.Step{
		ID:   "worktree",
		Name: "worktree",
		ExecutorConfig: ir.ExecutorConfig{
			Type: executorType,
		},
	}, NodeState{})

	err := NewStepExecutor().Execute(newTestStepExecutorContext(), node)
	require.ErrorContains(t, err, "failed to serialize outputs")
	require.Same(t, err, node.Error())
}

// A raw outputs payload the step cannot publish, because it is larger than the
// run allows, leaves the channel empty. The step's own work already succeeded,
// so it must not be reported as failed.
func TestStepExecutorDropsOversizedOutputsValue(t *testing.T) {
	executorType := "test-step-executor-oversized-outputs-value"
	runtimeexec.RegisterExecutor(executorType, func(context.Context, ir.Step) (runtimeexec.Executor, error) {
		return &outputsValueSideChannelExecutor{
			value: []map[string]any{{"RESULT": strings.Repeat("x", 128)}},
		}, nil
	}, nil, registry.ExecutorCapabilities{})
	t.Cleanup(func() { runtimeexec.UnregisterExecutor(executorType) })

	node := NewNode(ir.Step{
		ID:   "fan_out",
		Name: "fan_out",
		ExecutorConfig: ir.ExecutorConfig{
			Type: executorType,
		},
	}, NodeState{})

	ctx := NewContext(context.Background(), &ir.DAG{MaxOutputSize: 16}, "run-1", "dag.log")
	require.NoError(t, NewStepExecutor().Execute(ctx, node))
	require.NoError(t, node.Error())
	require.Nil(t, node.State().OutputsValue)
}

func TestStepExecutorPublishesOutputsValue(t *testing.T) {
	executorType := "test-step-executor-outputs-value"
	runtimeexec.RegisterExecutor(executorType, func(context.Context, ir.Step) (runtimeexec.Executor, error) {
		return &outputsValueSideChannelExecutor{
			value: []map[string]any{{"RESULT": "a"}, {"RESULT": "b"}},
		}, nil
	}, nil, registry.ExecutorCapabilities{})
	t.Cleanup(func() { runtimeexec.UnregisterExecutor(executorType) })

	node := NewNode(ir.Step{
		ID:   "fan_out",
		Name: "fan_out",
		ExecutorConfig: ir.ExecutorConfig{
			Type: executorType,
		},
	}, NodeState{})

	require.NoError(t, NewStepExecutor().Execute(newTestStepExecutorContext(), node))
	state := node.State()
	require.NotNil(t, state.OutputsValue)
	require.Equal(t, `[{"RESULT":"a"},{"RESULT":"b"}]`, *state.OutputsValue)
	require.Nil(t, state.StepOutputsValue)
}

func TestStepExecutorRecordsTimeoutBeforeCommandStarts(t *testing.T) {
	executorType := "test-step-executor-pre-run-timeout"
	runtimeexec.RegisterExecutor(executorType, func(ctx context.Context, _ ir.Step) (runtimeexec.Executor, error) {
		<-ctx.Done()
		return &emptySideChannelExecutor{}, nil
	}, nil, registry.ExecutorCapabilities{})
	t.Cleanup(func() { runtimeexec.UnregisterExecutor(executorType) })

	node := NewNode(ir.Step{
		Name: "pre-run-timeout-step",
		ExecutorConfig: ir.ExecutorConfig{
			Type: executorType,
		},
		Timeout: 10 * time.Millisecond,
	}, NodeState{})

	err := NewStepExecutor().Execute(newTestStepExecutorContext(), node)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "step timed out")
	require.Equal(t, ir.NodeFailed, node.Status())
	require.Equal(t, 124, node.GetExitCode())
}

func newTestStepExecutorContext() context.Context {
	return NewContext(context.Background(), &ir.DAG{}, "run-1", "dag.log")
}

// An executor that declares outputs but publishes none must leave the step
// output channels empty rather than storing a null payload.
func TestStepExecutorSkipsEmptyExecutorDeclaredOutputs(t *testing.T) {
	executorType := "test-step-executor-empty-declared-outputs"
	runtimeexec.RegisterExecutor(executorType, func(context.Context, ir.Step) (runtimeexec.Executor, error) {
		return &emptyDeclaredSideChannelExecutor{}, nil
	}, nil, registry.ExecutorCapabilities{})
	t.Cleanup(func() { runtimeexec.UnregisterExecutor(executorType) })

	node := NewNode(ir.Step{
		Name: "empty-declared-outputs-step",
		ExecutorConfig: ir.ExecutorConfig{
			Type: executorType,
		},
	}, NodeState{})

	require.NoError(t, NewStepExecutor().Execute(newTestStepExecutorContext(), node))

	require.Nil(t, node.State().StepOutputsValue)
	require.Nil(t, node.State().OutputsValue)
}
