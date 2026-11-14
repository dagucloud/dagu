// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/llm"
	runtimeexec "github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// modelCallingExecutor stands for a step that calls models: its run reports
// each call's usage through the run's context, as a provider does, and then
// waits to be released when release is set.
type modelCallingExecutor struct {
	emptySideChannelExecutor
	calls   []llm.UsageRecord
	called  chan struct{}
	release <-chan struct{}
}

func (e *modelCallingExecutor) Run(ctx context.Context) error {
	for _, call := range e.calls {
		llm.RecordUsage(ctx, call)
	}
	if e.called != nil {
		close(e.called)
	}
	if e.release == nil {
		return nil
	}
	select {
	case <-e.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func registerModelCallingExecutor(t *testing.T, newExecutor func() *modelCallingExecutor) string {
	t.Helper()
	executorType := "test-step-executor-model-usage-" + t.Name()
	runtimeexec.RegisterExecutor(executorType, func(context.Context, ir.Step) (runtimeexec.Executor, error) {
		return newExecutor(), nil
	}, nil, registry.ExecutorCapabilities{})
	t.Cleanup(func() { runtimeexec.UnregisterExecutor(executorType) })
	return executorType
}

func usageCall(model string, prompt int, cost *float64) llm.UsageRecord {
	return llm.UsageRecord{Provider: "openrouter", Model: model, Usage: llm.Usage{PromptTokens: prompt, CompletionTokens: 1, TotalTokens: prompt + 1, Cost: cost}}
}

// A step keeps every call its attempts made to models, on top of what its
// earlier attempts in the run used.
func TestStepExecutorRecordsModelUsage(t *testing.T) {
	cost := 0.001
	executorType := registerModelCallingExecutor(t, func() *modelCallingExecutor {
		return &modelCallingExecutor{calls: []llm.UsageRecord{
			usageCall("kitewell/included", 100, &cost),
			usageCall("kitewell/included", 50, &cost),
			usageCall("other", 7, nil),
		}}
	})
	node := NewNode(ir.Step{Name: "ask", ExecutorConfig: ir.ExecutorConfig{Type: executorType}}, NodeState{
		ModelUsage: &ir.StepModelUsage{Models: []ir.ModelUsage{{Provider: "openrouter", Model: "kitewell/included", Calls: 1, InputTokens: 10, OutputTokens: 1}}},
	})

	require.NoError(t, NewStepExecutor().Execute(newTestStepExecutorContext(), node))
	assert.Equal(t, []ir.ModelUsage{
		{Provider: "openrouter", Model: "kitewell/included", Calls: 3, InputTokens: 160, OutputTokens: 3, Cost: 0.002, CostReportedCalls: 2},
		{Provider: "openrouter", Model: "other", Calls: 1, InputTokens: 7, OutputTokens: 1},
	}, node.ModelUsage().Models)

	// A retry of the step in the run adds its calls.
	require.NoError(t, NewStepExecutor().Execute(newTestStepExecutorContext(), node))
	assert.Equal(t, int64(5), node.ModelUsage().Models[0].Calls)
}

// A step that reports no progress of its own has its model usage reported
// while it runs, and nothing after it ends.
func TestStepExecutorReportsModelUsageWhileRunning(t *testing.T) {
	previous := liveModelUsageInterval
	liveModelUsageInterval = 10 * time.Millisecond
	t.Cleanup(func() { liveModelUsageInterval = previous })

	release := make(chan struct{})
	called := make(chan struct{})
	executorType := registerModelCallingExecutor(t, func() *modelCallingExecutor {
		return &modelCallingExecutor{calls: []llm.UsageRecord{usageCall("m", 10, nil)}, called: called, release: release}
	})
	node := NewNode(ir.Step{Name: "ask", ExecutorConfig: ir.ExecutorConfig{Type: executorType}}, NodeState{})

	var progress atomic.Int32
	reported := make(chan *ir.StepModelUsage, 8)
	done := make(chan error, 1)
	go func() {
		done <- NewStepExecutor().ExecuteWithProgress(newTestStepExecutorContext(), node, nil, func() {
			progress.Add(1)
			reported <- node.ModelUsage()
		})
	}()

	<-called
	select {
	case usage := <-reported:
		require.NotNil(t, usage)
		assert.Equal(t, int64(1), usage.Models[0].Calls)
	case <-time.After(5 * time.Second):
		t.Fatal("the step's model usage was not reported while it ran")
	}
	close(release)
	require.NoError(t, <-done)

	after := progress.Load()
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, after, progress.Load(), "nothing is reported after the step ends")
}

// Resetting a step for another execution in the run keeps what its earlier
// attempts used on models.
func TestNodeResetsKeepModelUsage(t *testing.T) {
	t.Parallel()

	usage := &ir.StepModelUsage{Models: []ir.ModelUsage{{Provider: "openai", Model: "gpt", Calls: 2}}}
	step := ir.Step{Name: "ask"}

	retried := NewNode(step, NodeState{Status: ir.NodeFailed, ModelUsage: usage})
	retried.clearStateForRetry(step)
	assert.Equal(t, usage, retried.ModelUsage())

	rerun := NewNode(step, NodeState{Status: ir.NodeSucceeded, ModelUsage: usage})
	rerun.ResetForRerun(step)
	assert.Equal(t, usage, rerun.ModelUsage())
	assert.Equal(t, ir.NodeNotStarted, rerun.State().Status)
}
