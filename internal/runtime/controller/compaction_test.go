// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package controller_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/runtime/controller"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestState_CompactsOldObservationsFromDecisionTimeline(t *testing.T) {
	t.Parallel()

	dag := &core.DAG{Tasks: []core.ControllerTask{{Name: "done"}}}
	state := controller.NewState(dag)
	state.Events = []controller.Event{
		{Turn: 1, Kind: controller.EventAction, Name: "run_tests", Status: "failed", Reason: "exit status 1"},
		{Turn: 2, Kind: controller.EventTaskStatus, Name: "checked", Status: "completed", Reason: "tests passed"},
		{Turn: 3, Kind: controller.EventAction, Name: "publish", Status: "succeeded"},
		{Turn: 4, Kind: controller.EventAction, Name: "notify", Status: "succeeded"},
	}
	state.Append(
		assistantToolCall("call_1", "run_tests"),
		toolMessage("call_1", "status: failed\nerror: exit status 1\nlarge diagnostics"),
		assistantToolCall("call_2", controller.SetTaskStatusTool),
		toolMessage("call_2", "Task checked is now completed."),
		assistantToolCall("call_3", "publish"),
		toolMessage("call_3", "status: succeeded\noutputs: full recent output"),
		assistantToolCall("call_4", "notify"),
		toolMessage("call_4", "status: succeeded\noutput: newest output"),
	)

	state.EnableObservationAging()
	assert.Equal(t, 2, state.CompactObservations(2, core.DefaultControllerObservationMaxBytes))

	messages := state.Messages()
	assert.Equal(t, "turn 1: run_tests → failed (exit status 1)", messages[1].Content)
	assert.Equal(t, "turn 2: task checked → completed (tests passed)", messages[3].Content)
	assert.Equal(t, "status: succeeded\noutputs: full recent output", messages[5].Content)
	assert.Equal(t, "status: succeeded\noutput: newest output", messages[7].Content)
	assert.Equal(t, "call_1", messages[1].ToolCallID)
	assert.Equal(t, "call_2", messages[3].ToolCallID)
	assert.Zero(t, state.CompactObservations(2, core.DefaultControllerObservationMaxBytes),
		"compaction is idempotent")

	raw, err := state.Marshal()
	require.NoError(t, err)
	restored, err := controller.LoadState(raw, state.Messages(), dag)
	require.NoError(t, err)
	assert.True(t, restored.ObservationAging)
	assert.Equal(t, state.Messages(), restored.Messages())
}

func TestState_CompactsObservationWithoutTimelineEvent(t *testing.T) {
	t.Parallel()

	state := controller.NewState(&core.DAG{})
	state.Append(
		assistantToolCall("call_1", controller.SetTaskStatusTool),
		toolMessage("call_1", "Error: task is already open\nmore detail"),
		assistantToolCall("call_2", "next"),
		toolMessage("call_2", "status: succeeded"),
	)

	assert.Equal(t, 1, state.CompactObservations(1, core.DefaultControllerObservationMaxBytes))
	assert.Equal(t, "turn 1: set_task_status → rejected (task is already open)",
		state.Messages()[1].Content)
}

func TestState_ObservationAgingCanBeDisabled(t *testing.T) {
	t.Parallel()

	state := controller.NewState(&core.DAG{})
	state.Append(
		assistantToolCall("call_1", "first"),
		toolMessage("call_1", "full first result"),
		assistantToolCall("call_2", "second"),
		toolMessage("call_2", "full second result"),
	)

	assert.Zero(t, state.CompactObservations(0, core.DefaultControllerObservationMaxBytes))
	assert.Equal(t, "full first result", state.Messages()[1].Content)
}

func TestState_LatestPromptTokens(t *testing.T) {
	t.Parallel()

	state := controller.NewState(&core.DAG{})
	state.Append(
		exec.LLMMessage{Role: exec.RoleAssistant, Metadata: &exec.LLMMessageMetadata{PromptTokens: 20}},
		exec.LLMMessage{Role: exec.RoleTool, Content: "result"},
		exec.LLMMessage{Role: exec.RoleAssistant, Metadata: &exec.LLMMessageMetadata{PromptTokens: 35}},
		exec.LLMMessage{Role: exec.RoleTool, Content: "new result"},
		exec.LLMMessage{Role: exec.RoleAssistant, Metadata: &exec.LLMMessageMetadata{}},
	)
	assert.Equal(t, 35, state.LatestPromptTokens())
}

func assistantToolCall(id, name string) exec.LLMMessage {
	return exec.LLMMessage{
		Role: exec.RoleAssistant,
		ToolCalls: []exec.ToolCall{{
			ID:   id,
			Type: "function",
			Function: exec.ToolCallFunction{
				Name: name,
			},
		}},
	}
}

func toolMessage(id, content string) exec.LLMMessage {
	return exec.LLMMessage{Role: exec.RoleTool, ToolCallID: id, Content: content}
}
