// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package modelusage_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/runtime/modelusage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func call(model string, prompt, completion int, cost *float64) llm.UsageRecord {
	return llm.UsageRecord{Provider: "openrouter", Model: model, Usage: llm.Usage{
		PromptTokens: prompt, CompletionTokens: completion, TotalTokens: prompt + completion,
		CachedPromptTokens: prompt / 2, Cost: cost,
	}}
}

// A meter goes on from what the step used before and adds every call,
// counting cost only for the calls that reported one.
func TestMeter(t *testing.T) {
	t.Parallel()

	assert.Nil(t, modelusage.NewMeter(nil).Snapshot(), "nothing used")

	cost := 0.002
	meter := modelusage.NewMeter(&ir.StepModelUsage{Models: []ir.ModelUsage{
		{Provider: "openrouter", Model: "a", Calls: 1, InputTokens: 10, Cost: 0.001, CostReportedCalls: 1},
	}})
	meter.RecordLLMUsage(call("a", 100, 20, &cost))
	meter.RecordLLMUsage(call("a", 40, 4, nil))
	meter.RecordLLMUsage(call("b", 8, 2, nil))

	assert.Equal(t, &ir.StepModelUsage{Models: []ir.ModelUsage{
		{Provider: "openrouter", Model: "a", Calls: 3, InputTokens: 150, CachedInputTokens: 70, OutputTokens: 24, Cost: 0.003, CostReportedCalls: 2},
		{Provider: "openrouter", Model: "b", Calls: 1, InputTokens: 8, CachedInputTokens: 4, OutputTokens: 2},
	}}, meter.Snapshot())
}

// Calls made with a context carrying a meter add to it; a nil meter hides
// the one an outer context carries.
func TestWithMeter(t *testing.T) {
	t.Parallel()

	meter := modelusage.NewMeter(nil)
	ctx := modelusage.WithMeter(context.Background(), meter)
	llm.RecordUsage(ctx, call("a", 1, 1, nil))
	llm.RecordUsage(modelusage.WithMeter(ctx, nil), call("a", 1, 1, nil))
	assert.Same(t, meter, modelusage.FromContext(ctx))
	assert.Nil(t, modelusage.FromContext(modelusage.WithMeter(ctx, nil)))
	require.NotNil(t, meter.Snapshot())
	assert.Equal(t, int64(1), meter.Snapshot().Models[0].Calls)
}

// A flusher reports once an interval after calls, never without a new call,
// and never after it stops.
func TestFlusher(t *testing.T) {
	t.Parallel()

	meter := modelusage.NewMeter(nil)
	var reports atomic.Int32
	reported := make(chan *ir.StepModelUsage, 4)
	flusher := meter.StartFlusher(20*time.Millisecond, func(usage *ir.StepModelUsage) {
		reports.Add(1)
		reported <- usage
	})

	time.Sleep(60 * time.Millisecond)
	assert.Zero(t, reports.Load(), "no call, no report")

	meter.RecordLLMUsage(call("a", 1, 1, nil))
	meter.RecordLLMUsage(call("a", 1, 1, nil))
	select {
	case usage := <-reported:
		assert.Equal(t, int64(2), usage.Models[0].Calls, "calls within one interval make one report")
	case <-time.After(time.Second):
		t.Fatal("no report after calls")
	}
	time.Sleep(60 * time.Millisecond)
	assert.Equal(t, int32(1), reports.Load(), "no new call, no new report")

	flusher.Stop()
	meter.RecordLLMUsage(call("a", 1, 1, nil))
	time.Sleep(60 * time.Millisecond)
	assert.Equal(t, int32(1), reports.Load(), "nothing after Stop")
}

// Calls recorded from many goroutines all count.
func TestMeterConcurrentCalls(t *testing.T) {
	t.Parallel()

	meter := modelusage.NewMeter(nil)
	flusher := meter.StartFlusher(time.Millisecond, func(*ir.StepModelUsage) {})
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			for range 50 {
				meter.RecordLLMUsage(call("a", 1, 1, nil))
				_ = meter.Snapshot()
			}
		})
	}
	wg.Wait()
	flusher.Stop()
	assert.Equal(t, int64(1000), meter.Snapshot().Models[0].Calls)
}

// A source's total replaces the one it reported before, so a session that
// reports again, or a step that resumes it, never counts it twice; another
// source adds its own.
func TestMeterReported(t *testing.T) {
	t.Parallel()

	model := func(calls int64) []ir.ModelUsage {
		return []ir.ModelUsage{{Provider: "openai", Model: "gpt-5", Calls: calls, InputTokens: 10 * calls}}
	}
	meter := modelusage.NewMeter(&ir.StepModelUsage{Reported: []ir.ReportedModelUsage{{Source: "session-1", Models: model(2)}}})
	meter.SetReported("session-1", model(5))
	meter.SetReported("session-2", model(1))

	usage := meter.Snapshot()
	require.NotNil(t, usage)
	assert.Equal(t, []ir.ReportedModelUsage{{Source: "session-1", Models: model(5)}, {Source: "session-2", Models: model(1)}}, usage.Reported)
	assert.Equal(t, model(6), usage.Total())
}

// A tool whose usage cannot be read is named, and a step that only ran such a
// tool still has something to show.
func TestMeterNotReported(t *testing.T) {
	t.Parallel()

	meter := modelusage.NewMeter(nil)
	meter.MarkNotReported("claude")
	assert.Equal(t, &ir.StepModelUsage{NotReported: "claude"}, meter.Snapshot())

	var none *modelusage.Meter
	none.MarkNotReported("claude")
	none.SetReported("session", nil)
	none.RecordLLMUsage(call("a", 1, 1, nil))
	assert.Nil(t, none.Snapshot(), "a nil meter records nothing")
}

// Work the step finished elsewhere adds to its own usage, and a source that
// reports nothing leaves no entry behind.
func TestMeterAdd(t *testing.T) {
	t.Parallel()

	meter := modelusage.NewMeter(nil)
	meter.SetReported(modelusage.SubRunSource("child-1"), nil)
	assert.Nil(t, meter.Snapshot(), "a child that used no model adds no entry")

	item := []ir.ModelUsage{{Provider: "openai", Model: "gpt-5", Calls: 1, InputTokens: 5}}
	meter.Add(item)
	meter.Add(item)
	assert.Equal(t, &ir.StepModelUsage{Models: []ir.ModelUsage{{Provider: "openai", Model: "gpt-5", Calls: 2, InputTokens: 10}}}, meter.Snapshot())
}
