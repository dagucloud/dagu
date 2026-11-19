// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A configured label that starts with agent. stops the worker at start.
func TestWorkerLabelsRejectAgentLabels(t *testing.T) {
	t.Parallel()

	_, err := workerLabels(map[string]string{"agent.provider.claude": "true"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `worker label "agent.provider.claude" is reserved`)
	require.Error(t, CheckConfiguredLabels(map[string]string{"agent.x": "y"}))
	require.NoError(t, CheckConfiguredLabels(map[string]string{"zone": "eu"}))
}

// The agent mode sets one poller and announces the label of the provider.
func TestEnableAgentMode(t *testing.T) {
	t.Parallel()

	w := NewWorker("agent-1", 100, nil, map[string]string{"zone": "eu"}, nil)
	assert.Nil(t, w.Done(), "a general worker has no end of run")
	require.NoError(t, w.EnableAgentMode("claude"))
	assert.Equal(t, 1, w.maxActiveRuns)
	assert.Equal(t, "true", w.labels["agent.provider.claude"])
	assert.Equal(t, "eu", w.labels["zone"])
	assert.Equal(t, "claude", w.AgentProvider())
	assert.NotNil(t, w.Done())
	require.Error(t, NewWorker("x", 1, nil, nil, nil).EnableAgentMode("gemini"), "gemini is not a CLI provider")
}

// The final report records the last push of a final status of the run of the
// task, and nothing else.
func TestFinalReport(t *testing.T) {
	t.Parallel()

	report := newFinalReport("run-1")
	require.Error(t, report.result(), "no final status is a failed report")

	pusher := wrapFinalReport(withFinalReport(context.Background(), report), pushFunc(func(ir.DAGRunStatus) error { return nil }))
	require.NoError(t, pusher.Push(context.Background(), ir.DAGRunStatus{DAGRunID: "run-1", Status: ir.Running}))
	require.Error(t, report.result(), "a running status is not final")
	require.NoError(t, pusher.Push(context.Background(), ir.DAGRunStatus{DAGRunID: "child", Status: ir.Succeeded}))
	require.Error(t, report.result(), "the status of a child run does not count")
	require.NoError(t, pusher.Push(context.Background(), ir.DAGRunStatus{DAGRunID: "run-1", Status: ir.Failed}))
	require.NoError(t, report.result(), "the status of the run does not change the result")

	failing := wrapFinalReport(withFinalReport(context.Background(), report), pushFunc(func(ir.DAGRunStatus) error { return errors.New("unavailable") }))
	require.Error(t, failing.Push(context.Background(), ir.DAGRunStatus{DAGRunID: "run-1", Status: ir.Succeeded}))
	err := report.result()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unavailable")

	_, wrapped := wrapFinalReport(context.Background(), pushFunc(func(ir.DAGRunStatus) error { return nil })).(*finalReportPusher)
	assert.False(t, wrapped, "a context with no report keeps the pusher")
}

type pushFunc func(ir.DAGRunStatus) error

func (f pushFunc) Push(_ context.Context, status ir.DAGRunStatus) error { return f(status) }

// An agent worker runs only a DAG that needs its provider; a general worker runs
// no DAG that needs a provider.
func TestCheckAgentRouting(t *testing.T) {
	t.Parallel()

	claudeDAG := &ir.DAG{Steps: []ir.Step{{Name: "a", ID: "a", Agent: &ir.AgentRun{Provider: "claude"}}}}
	plainDAG := &ir.DAG{Steps: []ir.Step{{Name: "a", ID: "a"}}}

	require.NoError(t, checkAgentRouting(claudeDAG, "claude"))
	require.NoError(t, checkAgentRouting(plainDAG, ""))
	err := checkAgentRouting(claudeDAG, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `the DAG needs the provider "claude"`)
	require.Error(t, checkAgentRouting(plainDAG, "claude"))
	require.Error(t, checkAgentRouting(claudeDAG, "codex"))
}
