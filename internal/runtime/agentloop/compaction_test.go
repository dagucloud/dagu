// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agentloop_test

import (
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/agentloop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestState_CompactsOldObservationsFromDecisionTimeline(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{Tasks: []ir.AgentTask{{Name: "done"}}}
	state := agentloop.NewState(dag)
	state.Events = []agentloop.Event{
		{Turn: 1, Kind: agentloop.EventAction, Name: "run_tests", Status: "failed", Reason: "exit status 1"},
		{Turn: 2, Kind: agentloop.EventTaskStatus, Name: "checked", Status: "completed", Reason: "tests passed"},
		{Turn: 3, Kind: agentloop.EventAction, Name: "publish", Status: "succeeded"},
		{Turn: 4, Kind: agentloop.EventAction, Name: "notify", Status: "succeeded"},
	}
	state.Append(
		assistantToolCall("call_1", "run_tests"),
		toolMessage("call_1", "status: failed\nerror: exit status 1\nlarge diagnostics"),
		assistantToolCall("call_2", agentloop.SetTaskStatusTool),
		toolMessage("call_2", "Task checked is now completed."),
		assistantToolCall("call_3", "publish"),
		toolMessage("call_3", "status: succeeded\noutputs: full recent output"),
		assistantToolCall("call_4", "notify"),
		toolMessage("call_4", "status: succeeded\noutput: newest output"),
	)

	state.EnableObservationAging()
	assert.Equal(t, 2, state.CompactObservations(2, ir.DefaultAgentObservationMaxBytes))

	messages := state.Messages()
	assert.Equal(t, "turn 1: run_tests → failed (exit status 1)", messages[1].Content)
	assert.Equal(t, "turn 2: task checked → completed (tests passed)", messages[3].Content)
	assert.Equal(t, "status: succeeded\noutputs: full recent output", messages[5].Content)
	assert.Equal(t, "status: succeeded\noutput: newest output", messages[7].Content)
	assert.Equal(t, "call_1", messages[1].ToolCallID)
	assert.Equal(t, "call_2", messages[3].ToolCallID)
	assert.Zero(t, state.CompactObservations(2, ir.DefaultAgentObservationMaxBytes),
		"compaction is idempotent")

	raw, err := state.Marshal()
	require.NoError(t, err)
	restored, err := agentloop.LoadState(raw, state.Messages(), dag)
	require.NoError(t, err)
	assert.True(t, restored.ObservationAging)
	assert.Equal(t, state.Messages(), restored.Messages())
}

func TestState_CompactsParallelObservationsAgainstTheirOwnEvents(t *testing.T) {
	t.Parallel()

	state := agentloop.NewState(&ir.DAG{})
	state.Events = []agentloop.Event{
		{
			Turn: 1, Kind: agentloop.EventAction, Name: "alpha", Status: "succeeded",
			ToolCallID: "call_alpha",
		},
		{
			Turn: 1, Kind: agentloop.EventAction, Name: "beta", Status: "failed",
			Reason: "exit status 2", ToolCallID: "call_beta",
		},
	}
	state.Append(
		ir.LLMMessage{
			Role: ir.LLMRoleAssistant,
			ToolCalls: []ir.ToolCall{
				{ID: "call_alpha", Function: ir.ToolCallFunction{Name: "alpha"}},
				{ID: "call_beta", Function: ir.ToolCallFunction{Name: "beta"}},
			},
		},
		toolMessage("call_alpha", "status: succeeded\n"+strings.Repeat("alpha output ", 20)),
		toolMessage("call_beta", "status: failed\n"+strings.Repeat("beta output ", 20)),
	)

	assert.Equal(t, 2, state.CompactAllObservations(ir.DefaultAgentObservationMaxBytes))
	messages := state.Messages()
	assert.Equal(t, "turn 1: alpha → succeeded", messages[1].Content)
	assert.Equal(t, "turn 1: beta → failed (exit status 2)", messages[2].Content)
}

func TestState_CompactsObservationWithoutTimelineEvent(t *testing.T) {
	t.Parallel()

	state := agentloop.NewState(&ir.DAG{})
	state.Append(
		assistantToolCall("call_1", agentloop.SetTaskStatusTool),
		toolMessage("call_1", "Error: task is already open\nmore detail"),
		assistantToolCall("call_2", "next"),
		toolMessage("call_2", "status: succeeded"),
	)

	assert.Equal(t, 1, state.CompactObservations(1, ir.DefaultAgentObservationMaxBytes))
	assert.Equal(t, "turn 1: set_task_status → rejected (task is already open)",
		state.Messages()[1].Content)
	assert.Zero(t, state.CompactObservations(1, ir.DefaultAgentObservationMaxBytes),
		"compaction is idempotent without a timeline event")
	assert.Equal(t, "turn 1: set_task_status → rejected (task is already open)",
		state.Messages()[1].Content)
}

func TestState_ObservationAgingCanBeDisabled(t *testing.T) {
	t.Parallel()

	state := agentloop.NewState(&ir.DAG{})
	state.Append(
		assistantToolCall("call_1", "first"),
		toolMessage("call_1", "full first result"),
		assistantToolCall("call_2", "second"),
		toolMessage("call_2", "full second result"),
	)

	assert.Zero(t, state.CompactObservations(0, ir.DefaultAgentObservationMaxBytes))
	assert.Equal(t, "full first result", state.Messages()[1].Content)
}

func TestState_CompactionSizeLimitCanBeDisabled(t *testing.T) {
	t.Parallel()

	state := agentloop.NewState(&ir.DAG{})
	state.Events = []agentloop.Event{
		{Turn: 1, Kind: agentloop.EventAction, Name: "first", Status: "succeeded"},
	}
	state.Append(
		assistantToolCall("call_1", "first"),
		toolMessage("call_1", "status: succeeded\nlarge output"),
		assistantToolCall("call_2", "second"),
		toolMessage("call_2", "recent output"),
	)

	assert.Equal(t, 1, state.CompactObservations(1, 0))
	assert.Equal(t, "turn 1: first → succeeded", state.Messages()[1].Content)
}

func TestState_FallbackSummaryCountsProseDecisionsAsTurns(t *testing.T) {
	t.Parallel()

	state := agentloop.NewState(&ir.DAG{})
	state.Append(
		ir.LLMMessage{Role: ir.LLMRoleAssistant, Content: "I need another reminder."},
		ir.LLMMessage{Role: ir.LLMRoleUser, Content: "Choose an action."},
		assistantToolCall("call_2", "second"),
		toolMessage("call_2", "status: succeeded\nlarge output"),
		assistantToolCall("call_3", "third"),
		toolMessage("call_3", "recent output"),
	)

	assert.Equal(t, 1, state.CompactObservations(1, ir.DefaultAgentObservationMaxBytes))
	assert.Equal(t, "turn 2: second → succeeded", state.Messages()[3].Content)
}

func TestState_CompactsAllUsefulObservationsForOverflow(t *testing.T) {
	t.Parallel()

	state := agentloop.NewState(&ir.DAG{})
	state.Events = []agentloop.Event{
		{Turn: 1, Kind: agentloop.EventAction, Name: "first", Status: "succeeded"},
		{Turn: 2, Kind: agentloop.EventAction, Name: "second", Status: "succeeded"},
	}
	state.Append(
		assistantToolCall("call_1", "first"),
		toolMessage("call_1", "status: succeeded\n"+strings.Repeat("large output ", 20)),
		assistantToolCall("call_2", "second"),
		toolMessage("call_2", "ok"),
	)

	assert.Equal(t, 1, state.CompactAllObservations(ir.DefaultAgentObservationMaxBytes))
	assert.Equal(t, "turn 1: first → succeeded", state.Messages()[1].Content)
	assert.Equal(t, "ok", state.Messages()[3].Content)
}

func TestState_LatestPromptTokens(t *testing.T) {
	t.Parallel()

	state := agentloop.NewState(&ir.DAG{})
	state.Append(
		ir.LLMMessage{Role: ir.LLMRoleAssistant, Metadata: &ir.LLMMessageMetadata{PromptTokens: 20}},
		ir.LLMMessage{Role: ir.LLMRoleTool, Content: "result"},
		ir.LLMMessage{Role: ir.LLMRoleAssistant, Metadata: &ir.LLMMessageMetadata{PromptTokens: 35}},
		ir.LLMMessage{Role: ir.LLMRoleTool, Content: "new result"},
		ir.LLMMessage{Role: ir.LLMRoleAssistant, Metadata: &ir.LLMMessageMetadata{}},
	)
	assert.Equal(t, 35, state.LatestPromptTokens())
}

func assistantToolCall(id, name string) ir.LLMMessage {
	return ir.LLMMessage{
		Role: ir.LLMRoleAssistant,
		ToolCalls: []ir.ToolCall{{
			ID:   id,
			Type: "function",
			Function: ir.ToolCallFunction{
				Name: name,
			},
		}},
	}
}

func toolMessage(id, content string) ir.LLMMessage {
	return ir.LLMMessage{Role: ir.LLMRoleTool, ToolCallID: id, Content: content}
}
