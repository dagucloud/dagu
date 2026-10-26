// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package foreach

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	runtimeexec "github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/runtime/modelusage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// modelCallExecutor stands for a body step that calls a model once.
type modelCallExecutor struct{}

func (modelCallExecutor) SetStdout(io.Writer)  {}
func (modelCallExecutor) SetStderr(io.Writer)  {}
func (modelCallExecutor) Kill(os.Signal) error { return nil }
func (modelCallExecutor) Run(ctx context.Context) error {
	llm.RecordUsage(ctx, llm.UsageRecord{Provider: "openrouter", Model: "kitewell/included", Usage: llm.Usage{PromptTokens: 30, CompletionTokens: 2, TotalTokens: 32}})
	return nil
}

// The body steps of a foreach are not saved with the run, so what they used
// on models counts toward the foreach step, every item's.
func TestForeachCountsBodyModelUsage(t *testing.T) {
	executorType := "test-foreach-model-call"
	runtimeexec.RegisterExecutor(executorType, func(context.Context, ir.Step) (runtimeexec.Executor, error) {
		return modelCallExecutor{}, nil
	}, nil, registry.ExecutorCapabilities{})
	t.Cleanup(func() { runtimeexec.UnregisterExecutor(executorType) })

	e := &foreachExecutor{step: ir.Step{
		Name: "each",
		Foreach: &ir.ForeachConfig{
			Items: []any{"a", "b", "c"},
			Steps: []ir.Step{{Name: "ask", ID: "ask", ExecutorConfig: ir.ExecutorConfig{Type: executorType}}},
		},
	}}
	e.SetStdout(io.Discard)
	meter := modelusage.NewMeter(nil)
	ctx := modelusage.WithMeter(runtime.NewContext(context.Background(), &ir.DAG{Name: "each"}, "run-1", ""), meter)

	require.NoError(t, e.Run(ctx))
	usage := meter.Snapshot()
	require.NotNil(t, usage)
	assert.Equal(t, []ir.ModelUsage{{Provider: "openrouter", Model: "kitewell/included", Calls: 3, InputTokens: 90, OutputTokens: 6}}, usage.Total())
}
