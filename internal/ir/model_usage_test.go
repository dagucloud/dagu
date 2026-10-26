// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
)

// Usage of the same provider and model adds up into one entry, and entries
// come out ordered by provider and model.
func TestMergeModelUsage(t *testing.T) {
	t.Parallel()

	merged := ir.MergeModelUsage(
		[]ir.ModelUsage{{Provider: "openrouter", Model: "b", Calls: 1, InputTokens: 10, Cost: 0.01, CostReportedCalls: 1}},
		ir.ModelUsage{Provider: "anthropic", Model: "a", Calls: 2, OutputTokens: 5},
		ir.ModelUsage{Provider: "openrouter", Model: "b", Calls: 1, InputTokens: 20, CachedInputTokens: 15},
	)
	assert.Equal(t, []ir.ModelUsage{
		{Provider: "anthropic", Model: "a", Calls: 2, OutputTokens: 5},
		{Provider: "openrouter", Model: "b", Calls: 2, InputTokens: 30, CachedInputTokens: 15, Cost: 0.01, CostReportedCalls: 1},
	}, merged)
}

// A run's total covers its steps and its handlers, skips a missing node, and
// is nil when nothing reports usage.
func TestModelUsageTotal(t *testing.T) {
	t.Parallel()

	usage := func(calls int64) *ir.StepModelUsage {
		return &ir.StepModelUsage{Models: []ir.ModelUsage{{Provider: "openai", Model: "gpt", Calls: calls, InputTokens: 100 * calls}}}
	}
	status := ir.DAGRunStatus{
		Nodes:     []*ir.Node{{ModelUsage: usage(1)}, nil, {}, {ModelUsage: usage(2)}},
		OnFailure: &ir.Node{ModelUsage: usage(4)},
	}
	assert.Equal(t, []ir.ModelUsage{{Provider: "openai", Model: "gpt", Calls: 7, InputTokens: 700}}, status.ModelUsageTotal())
	assert.Nil(t, (&ir.DAGRunStatus{Nodes: []*ir.Node{{}}}).ModelUsageTotal())
}

// BenchmarkModelUsageTotal measures the total a run's detail computes on
// every read, for a large run whose every step used two models.
func BenchmarkModelUsageTotal(b *testing.B) {
	status := ir.DAGRunStatus{Nodes: make([]*ir.Node, 500)}
	for i := range status.Nodes {
		status.Nodes[i] = &ir.Node{ModelUsage: &ir.StepModelUsage{Models: []ir.ModelUsage{
			{Provider: "openrouter", Model: "kitewell/included", Calls: 3, InputTokens: 1200, OutputTokens: 80, Cost: 0.001, CostReportedCalls: 3},
			{Provider: "anthropic", Model: "claude-sonnet", Calls: 1, InputTokens: 900, OutputTokens: 40},
		}}}
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = status.ModelUsageTotal()
	}
}
